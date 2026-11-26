// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/mattn/go-shellwords"
)

const defaultTimeout = 60

const imageMagickNotFound = "ImageMagick is required for image processing but was not found on this host. " +
	"Install it — Windows: winget install ImageMagick.ImageMagick; macOS: brew install imagemagick; Linux: apt-get install imagemagick. " +
	"Neither the `magick` (ImageMagick 7) nor `convert` (ImageMagick 6) binary is on PATH."

const ghostscriptNotFound = "Ghostscript is required for ImageMagick to read or write PDF files but was not found on this host. " +
	"Install it — Windows: winget install ArtifexSoftware.GhostScript; macOS: brew install ghostscript; Linux: apt-get install ghostscript."

var resolveBinary = sync.OnceValue(func() string {
	if _, err := exec.LookPath("magick"); err == nil {
		return "magick"
	}
	if _, err := exec.LookPath("convert"); err == nil {
		return "convert"
	}
	return ""
})

func parseArgs(cmd string) ([]string, error) {
	parser := shellwords.NewParser()
	args, err := parser.Parse(cmd)
	if err != nil {
		return nil, fmt.Errorf("parse command: %w", err)
	}
	if parser.Position >= 0 {
		return nil, unquotedOperatorError(cmd, parser.Position)
	}
	return args, nil
}

func unquotedOperatorError(cmd string, position int) error {
	runes := []rune(cmd)
	offset := position
	for offset < len(runes)-1 && unicode.IsDigit(runes[offset]) {
		offset++
	}
	return fmt.Errorf("unquoted %q in %q at offset %d: the command does not run through a shell, so leave out redirection and pipes such as 2>&1, > file or | cmd, and quote an argument that contains the character, for example \"800x600>\"",
		string(runes[offset]), wordAround(runes, offset), offset)
}

func wordAround(runes []rune, i int) string {
	start, end := i, i+1
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	return string(runes[start:end])
}

func substitutePlaceholders(tokens, inputPaths []string, outputPath, outputDir string) []string {
	pairs := make([]string, 0, 2*len(inputPaths)+4)
	for i, path := range inputPaths {
		pairs = append(pairs, fmt.Sprintf("{input[%d]}", i), path)
	}
	pairs = append(pairs, "{output}", outputPath, "{output_dir}", outputDir)
	replacer := strings.NewReplacer(pairs...)
	args := make([]string, len(tokens))
	for i, token := range tokens {
		args[i] = replacer.Replace(token)
	}
	return args
}

func unknownInputPlaceholder(tokens []string, inputs int) string {
	pairs := make([]string, 0, 2*inputs)
	for i := range inputs {
		pairs = append(pairs, fmt.Sprintf("{input[%d]}", i), "")
	}
	valid := strings.NewReplacer(pairs...)
	for _, token := range tokens {
		token = valid.Replace(token)
		start := strings.Index(token, "{input[")
		if start < 0 {
			continue
		}
		if end := strings.Index(token[start:], "]}"); end >= 0 {
			return token[start : start+end+2]
		}
		return token[start:]
	}
	return ""
}

func displayCommand(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if arg != "" && !strings.ContainsAny(arg, " \t\n'\"\\$`;&|<>()*?[]{}#~!") {
			quoted[i] = arg
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func touchesPDF(p Params) bool {
	if strings.HasSuffix(strings.ToLower(p.OutputFilename), ".pdf") {
		return true
	}
	for _, art := range p.InputImages {
		if strings.HasSuffix(strings.ToLower(art.Filename), ".pdf") {
			return true
		}
	}
	if args, err := parseArgs(p.Command); err == nil {
		for _, a := range args {
			if strings.HasSuffix(strings.ToLower(a), ".pdf") {
				return true
			}
		}
	}
	return false
}

func ghostscriptAvailable() bool {
	for _, name := range []string{"gs", "gswin64c", "gswin32c"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

const instructions = `Pass the ImageMagick arguments without the binary name. The tool runs ` + "`magick`" + ` (IM7) or ` + "`convert`" + ` (IM6), whichever the sandbox has, and reports it as ` + "`binary_used`" + `. IM6 has no subcommands such as ` + "`mogrify`" + ` or ` + "`identify`" + `, so write one ` + "`convert`" + `-style pass instead, and rewrite a command in IM6 syntax when the result gives that hint.

- Reference files only through ` + "`{input[N]}`" + `, ` + "`{output}`" + ` and ` + "`{output_dir}`" + `, and set ` + "`output_filename`" + ` to a bare file name, or a pattern such as ` + "`frame_%04d.png`" + ` with ` + "`{output_dir}`" + ` for several outputs.
- Quote any argument containing ; & | < or >, for example ` + "`-resize \"800x600>\"`" + `. There is no shell, so leave out pipes and redirection.
- Rejected: absolute, ` + "`~`" + ` or ` + "`..`" + ` paths, arguments starting with ` + "`@`" + ` or ` + "`|`" + `, ` + "`-script`" + `, URL and script coders (` + "`https:`" + `, ` + "`url:`" + `, ` + "`inline:`" + `, ` + "`msl:`" + `, ` + "`mvg:`" + `) and ` + "`text:`" + ` unless it reads an input (` + "`text:{input[0]}`" + `). Pass a web image in as an input artifact.
- Keep work small: ImageMagick runs under memory, pixel-count and time limits, and the process is killed when it exceeds them. Use ` + "`-density`" + ` at or below 150, ` + "`-resize`" + ` large inputs first, and select pages with ` + "`{input[0]}[0-4]`" + `. For PDF pages prefer ` + "`render_document_to_images`" + `. If the result says timed out or killed, retry with a smaller job rather than the same command.
- Fonts by name: Liberation-Sans, Liberation-Serif, Liberation-Mono, DejaVu-Sans, Noto-Sans, Noto-Serif; Arial, Helvetica, Times and Courier map to Liberation. Add ` + "`-Bold`" + ` for bold.

Examples: ` + "`{input[0]} -resize 800x600 {output}`" + `, ` + "`{input[0]} -gravity south -annotate +0+10 \"Q3\" {output}`" + `, ` + "`-size 800x600 canvas:white -fill red -draw \"circle 400,300 400,100\" {output}`" + `.
`

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("image_magick",
			"Executes ImageMagick commands for image manipulation — resize, crop, convert, composite, annotate, apply effects, and more. Uses either ImageMagick 7 (`magick`) or ImageMagick 6 (`convert`) depending on what the sandbox provides; the binary actually used is reported back in the result.",
			executeImageMagick,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

type Params struct {
	Command        string         `json:"command" desc:"The ImageMagick command arguments (without 'magick' prefix). Use {input[N]} for input artifacts, {output} for the output path, and {output_dir} for the output directory."`
	InputImages    []sdk.Artifact `json:"input_images" desc:"Input image artifacts to process. Referenced in the command as {input[0]}, {input[1]}, etc. Send an empty array [] for commands that create images from scratch (e.g., canvas creation)."`
	OutputFilename string         `json:"output_filename" desc:"Filename for the output (e.g., 'result.png', 'output.webp'). The extension determines the output format. For multi-file output, use a pattern like 'frame_%04d.png'."`
	TimeoutSeconds *int           `json:"timeout_seconds" desc:"Maximum execution time in seconds. Default is 60."`
}

func logRejection(tc *sdk.ToolContext, rule string) {
	slog.Default().With("logger_name", "imagemagick").Warn("imagemagick call rejected", "taskID", tc.TaskID, "rule", rule)
}

func executeImageMagick(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.Command == "" {
		return sdk.Error("command is required"), nil
	}
	if p.OutputFilename == "" {
		return sdk.Error("output_filename is required"), nil
	}
	if !isPlainFilename(p.OutputFilename) {
		logRejection(tc, "output_filename")
		return sdk.Error(fmt.Sprintf("output_filename %q must be a file name without directories", p.OutputFilename)), nil
	}

	workDir, err := os.MkdirTemp("", workDirPattern)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create temp directory: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	inputDir := filepath.Join(workDir, "input")
	outputDir := filepath.Join(workDir, "output")
	if err = os.MkdirAll(inputDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create input directory: %v", err)), nil
	}
	if err = os.MkdirAll(outputDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create output directory: %v", err)), nil
	}

	inputPaths := make([]string, len(p.InputImages))
	for i, art := range p.InputImages {
		filename := inputFilename(art.Filename, i)
		if v := validateInputName(filename); v != nil {
			logRejection(tc, v.rule)
			return sdk.Error(fmt.Sprintf("invalid input artifact %d: %v", i, v)), nil
		}
		artDir := filepath.Join(inputDir, strconv.Itoa(i))
		if err = os.MkdirAll(artDir, 0o755); err != nil {
			return sdk.Error(fmt.Sprintf("failed to create input directory: %v", err)), nil
		}
		path := filepath.Join(artDir, filename)
		if err = os.WriteFile(path, art.AsBytes(), 0o644); err != nil {
			return sdk.Error(fmt.Sprintf("failed to write input artifact %d: %v", i, err)), nil
		}
		inputPaths[i] = path
	}

	outputPath := filepath.Join(outputDir, p.OutputFilename)

	tokens, err := parseArgs(p.Command)
	if err != nil {
		return sdk.Error(fmt.Sprintf("invalid command: %v", err)), nil
	}
	if placeholder := unknownInputPlaceholder(tokens, len(inputPaths)); placeholder != "" {
		return sdk.Error(fmt.Sprintf("command references %s but only %d input image(s) were provided", placeholder, len(p.InputImages))), nil
	}
	if v := validateArgs(tokens); v != nil {
		logRejection(tc, v.rule)
		return sdk.Error(fmt.Sprintf("invalid command: %v", v)), nil
	}
	args := substitutePlaceholders(tokens, inputPaths, outputPath, outputDir)

	timeout := defaultTimeout
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds > 0 {
		timeout = *p.TimeoutSeconds
	}

	binary := resolveBinary()
	if binary == "" {
		return sdk.Error(
			imageMagickNotFound,
			sdk.WithData(map[string]any{
				"binary_used": "",
			}),
		), nil
	}

	if touchesPDF(p) && !ghostscriptAvailable() {
		return sdk.Error(ghostscriptNotFound), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("Running ImageMagick command (using %q)...", binary))

	env, err := policyEnv(ctx, binary, workDir, timeout)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to write the ImageMagick policy: %v", err)), nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	fullCmd := displayCommand(append([]string{binary}, args...))
	execCmd := exec.CommandContext(timeoutCtx, binary, args...)
	execCmd.Dir = workDir
	execCmd.Env = env
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	execErr := execCmd.Run()
	if execErr != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = execErr.Error()
		}

		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(execErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}

		data := map[string]any{
			"command":     fullCmd,
			"binary_used": binary,
			"stderr":      errMsg,
			"stdout":      stdout.String(),
			"exit_code":   exitCode,
		}
		if hint := im6SyntaxHint(binary, p.Command, errMsg); hint != "" {
			data["hint"] = hint
		}

		msg := failureMessage(binary, exitCode, execErr, ctx.Err(), timeoutCtx.Err(), timeout, errMsg)
		if hint, ok := data["hint"].(string); ok {
			msg += ". " + hint
		}

		return sdk.Error(msg, sdk.WithData(data)), nil
	}

	outputFiles, err := collectOutputFiles(outputDir)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to read output files: %v", err)), nil
	}

	if len(outputFiles) == 0 {
		msg := fmt.Sprintf("ImageMagick command completed successfully (binary=%q) but produced no output files.", binary)
		if out := stdout.String(); out != "" {
			msg += "\n\nOutput:\n" + out
		}
		return sdk.OK(msg, sdk.WithData(map[string]any{"binary_used": binary})), nil
	}

	dataObjects := make([]sdk.DataObject, 0, len(outputFiles))
	for _, f := range outputFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			return sdk.Error(fmt.Sprintf("failed to read output file %s: %v", filepath.Base(f), err)), nil
		}

		filename := filepath.Base(f)
		mimeType := detectMIMEType(filename)

		dataObjects = append(dataObjects, sdk.DataObject{
			Name:        filename,
			Content:     content,
			MIMEType:    mimeType,
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("ImageMagick output: %s", filename),
		})
	}

	msg := fmt.Sprintf("ImageMagick command completed successfully (binary=%q). Produced %d output file(s).", binary, len(dataObjects))
	if out := stdout.String(); out != "" {
		msg += "\n\nOutput:\n" + out
	}

	return sdk.OK(msg,
		sdk.WithData(map[string]any{"binary_used": binary}),
		sdk.WithDataObjects(dataObjects...),
	), nil
}

const killedHint = "Try a smaller -density, -resize the input first, process fewer pages (for example {input[0]}[0-4]), or for PDF pages use render_document_to_images."

const limitHint = "A resource limit (memory, pixel count, disk or time) was hit. " + killedHint

const sigXFSZ = syscall.Signal(25)

var resourceLimitStderr = []string{"cache resources exhausted", "time limit exceeded", "resource limit", "memory allocation failed"}

func hitResourceLimit(stderr string) bool {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "exceeds") && strings.Contains(lower, "limit") {
		return true
	}
	for _, marker := range resourceLimitStderr {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func failureMessage(binary string, exitCode int, execErr, callerErr, timeoutErr error, timeout int, stderr string) string {
	if callerErr != nil {
		return fmt.Sprintf("ImageMagick command was stopped before it finished (binary=%q): %v", binary, callerErr)
	}
	if errors.Is(timeoutErr, context.DeadlineExceeded) {
		return fmt.Sprintf("ImageMagick command timed out after %ds (binary=%q). %s", timeout, binary, killedHint)
	}
	var exitErr *exec.ExitError
	if errors.As(execErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			signal := status.Signal()
			switch signal {
			case sigXFSZ:
				return fmt.Sprintf("ImageMagick command was killed by signal %d: an output or cache file grew too large (binary=%q). %s", int(signal), binary, killedHint)
			case syscall.SIGKILL:
				return fmt.Sprintf("ImageMagick command was killed by signal %d (%s), likely out of memory (binary=%q). %s", int(signal), signal, binary, killedHint)
			}
			return fmt.Sprintf("ImageMagick command was killed by signal %d (%s) (binary=%q). %s", int(signal), signal, binary, killedHint)
		}
	}
	msg := fmt.Sprintf("ImageMagick command failed (binary=%q, exit code %d)", binary, exitCode)
	if hitResourceLimit(stderr) {
		msg += ". " + limitHint
	}
	return msg
}

func im6SyntaxHint(binary, cmd, stderr string) string {
	if binary != "convert" {
		return ""
	}

	firstTok := ""
	if parts := strings.Fields(cmd); len(parts) > 0 {
		firstTok = parts[0]
	}
	switch firstTok {
	case "mogrify", "identify", "composite", "montage", "compare", "conjure", "stream", "animate", "display", "import":
		return fmt.Sprintf(
			"This sandbox has ImageMagick 6 (no `magick` binary). The command started with %q, which is an IM7-style subcommand. Under IM6 those are separate binaries and this tool only invokes `convert`. Rewrite the operation as a single `convert`-style pipeline (e.g. `{input[0]} -resize 50%% {output}` instead of `mogrify -resize 50%% {input[0]}`).",
			firstTok,
		)
	}

	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "unrecognized option") ||
		strings.Contains(lower, "unknown option") ||
		strings.Contains(lower, "no such image operator") ||
		strings.Contains(lower, "unrecognized color") {
		return "This sandbox has ImageMagick 6 (`convert`), not ImageMagick 7 (`magick`). The stderr above suggests an IM7-only flag or operator. Re-issue the command using IM6-compatible syntax."
	}
	return ""
}

func collectOutputFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func detectMIMEType(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return "application/octet-stream"
	}

	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		return mimeType
	}

	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".tiff", ".tif":
		return "image/tiff"
	case ".ico":
		return "image/x-icon"
	case ".avif":
		return "image/avif"
	case ".heic", ".heif":
		return "image/heic"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}
