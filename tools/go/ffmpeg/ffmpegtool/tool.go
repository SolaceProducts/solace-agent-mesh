// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ffmpegtool

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	shellwords "github.com/mattn/go-shellwords"
)

const defaultFFmpegTimeout = 300
const defaultFFprobeTimeout = 30

const ffmpegNotFound = "FFmpeg is required for audio/video processing but was not found on this host. " +
	"Install it — Windows: winget install Gyan.FFmpeg; macOS: brew install ffmpeg; Linux: apt-get install ffmpeg."

const (
	ffmpegQuotingExample  = `-filter_complex "[0:v]A;[1:a]B"`
	ffprobeQuotingExample = ""
)

func parseArgs(cmd, quotingExample string) ([]string, error) {
	parser := shellwords.NewParser()
	args, err := parser.Parse(cmd)
	if err != nil {
		return nil, fmt.Errorf("parse command: %w", err)
	}
	if parser.Position >= 0 {
		return nil, unquotedOperatorError(cmd, parser.Position, quotingExample)
	}
	return args, nil
}

func unquotedOperatorError(cmd string, position int, quotingExample string) error {
	runes := []rune(cmd)
	offset := position
	for offset < len(runes)-1 && unicode.IsDigit(runes[offset]) {
		offset++
	}
	example := ""
	if quotingExample != "" {
		example = ", for example " + quotingExample
	}
	return fmt.Errorf("unquoted %q in %q at offset %d: the command does not run through a shell, so leave out redirection and pipes such as 2>&1, > file or | cmd, and quote an argument that contains the character%s",
		string(runes[offset]), wordAround(runes, offset), offset, example)
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

const ffmpegInstructions = `Pass the arguments without ` + "`ffmpeg`" + `; ` + "`-y`" + ` is added. Reference files only through ` + "`{input[N]}`" + `, ` + "`{output}`" + ` and ` + "`{output_dir}`" + `. Inputs share one directory under their own file names, so a filter can name one directly (` + "`subtitles=subs.srt`" + `, ` + "`fontfile=font.ttf`" + `); a later input with the same name is prefixed with its index and the result says so.

- Quote any argument containing ; & | < or >, for example ` + "`-filter_complex \"[0:v]A;[1:a]B\"`" + `. There is no shell, so leave out pipes and redirection.
- Rejected: URLs and network protocols, absolute, home or ` + "`..`" + ` paths, playlists and manifests (.m3u8, .m3u, .mpd, .sdp, or ` + "`-f hls`" + `, dash, sdp, rtp, rtsp), capture devices, ` + "`-filter_script`" + `, ` + "`-filter_complex_script`" + `, ` + "`-protocol_whitelist`" + `, ` + "`-protocol_blacklist`" + `, ` + "`-dump_attachment`" + `, options written as ` + "`-/option`" + `, the concat-list ` + "`option`" + ` directive, and the frei0r, ladspa, lv2, zmq, azmq, sendcmd and asendcmd filters. Fetch remote media with a web tool first.
- A concat list (` + "`-f concat -safe 0 -i {input[0]}`" + `) names the other inputs by file name, such as ` + "`file 'part1.mp4'`" + `.
- FFmpeg logs to stderr even on success; only a non-zero exit code is a failure. A successful result means every output file is saved as an artifact, so it needs no ffprobe check.

Examples: ` + "`-i {input[0]} -vf scale=1280:720 {output}`" + `, ` + "`-i {input[0]} -vn -acodec libmp3lame {output}`" + ` with ` + "`output_filename`" + ` ` + "`audio.mp3`" + `, ` + "`-f lavfi -i anullsrc=r=44100:cl=stereo -t 5 {output}`" + `, and a still image to video: ` + "`-loop 1 -i {input[0]} -t 5 -c:v libx264 -pix_fmt yuv420p {output}`" + `.
`

const ffprobeInstructions = `Returns the file's format and streams as JSON; ` + "`-v quiet -print_format json -show_format -show_streams`" + ` is always added. ` + "`command`" + ` adds arguments such as ` + "`-show_entries stream=codec_name,width,height,duration`" + `, under the ffmpeg tool's rules: no URLs, absolute or ` + "`..`" + ` paths, or playlists, and quote any argument containing ; & | < or >.
`

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("ffmpeg",
			"Executes FFmpeg commands for audio/video processing — convert, resize, trim, merge, extract, apply filters, and more",
			executeFFmpeg,
			sdk.WithInstructions(ffmpegInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("ffprobe",
			"Inspects media file metadata using FFprobe — codecs, resolution, duration, bitrate, streams, and more",
			executeFFprobe,
			sdk.WithInstructions(ffprobeInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	}
}

type FFmpegParams struct {
	Command        string         `json:"command" desc:"The FFmpeg command arguments (without 'ffmpeg' prefix). Use {input[N]} for input artifacts, {output} for the output path, and {output_dir} for the output directory. The -y flag is added automatically."`
	InputFiles     []sdk.Artifact `json:"input_files" desc:"Input media artifacts to process. Referenced in the command as {input[0]}, {input[1]}, etc. Send an empty array [] for commands that generate media from scratch (e.g., generating silence, test patterns)."`
	OutputFilename string         `json:"output_filename" desc:"Filename for the output (e.g., 'output.mp4', 'audio.mp3'). The extension determines the container format. For multi-file output, use a pattern like 'frame_%04d.png'."`
	TimeoutSeconds *int           `json:"timeout_seconds" desc:"Maximum execution time in seconds. Default is 300 (5 minutes)."`
}

type FFprobeParams struct {
	InputFile      sdk.Artifact `json:"input_file" desc:"The media artifact to inspect."`
	Command        *string      `json:"command" desc:"Additional ffprobe arguments. Default behavior shows full format and stream info as JSON."`
	TimeoutSeconds *int         `json:"timeout_seconds" desc:"Maximum execution time in seconds. Default is 30."`
}

func executeFFmpeg(ctx context.Context, p FFmpegParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.Command == "" {
		return sdk.Error("command is required"), nil
	}
	if p.OutputFilename == "" {
		return sdk.Error("output_filename is required"), nil
	}
	if !isPlainFilename(p.OutputFilename) {
		logRejection(tc, "ffmpeg", "output_filename")
		return sdk.Error("output_filename must be a file name without directories"), nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return sdk.Error(ffmpegNotFound), nil
	}

	tokens, err := parseArgs(p.Command, ffmpegQuotingExample)
	if err != nil {
		return sdk.Error(fmt.Sprintf("invalid command: %v", err)), nil
	}
	if placeholder := unknownInputPlaceholder(tokens, len(p.InputFiles)); placeholder != "" {
		return sdk.Error(fmt.Sprintf("command references %s but only %d input file(s) were provided", placeholder, len(p.InputFiles))), nil
	}
	if v := validateArgs(tokens); v != nil {
		logRejection(tc, "ffmpeg", v.rule)
		return sdk.Error(fmt.Sprintf("invalid command: %v", v)), nil
	}

	workDir, err := os.MkdirTemp("", "ffmpeg-*")
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

	concat := concatInputs(tokens)
	taken := map[string]bool{}
	var renamed []string
	inputPaths := make([]string, len(p.InputFiles))
	for i, art := range p.InputFiles {
		filename, clash := inputFilename(art.Filename, i, taken)
		if clash {
			renamed = append(renamed, fmt.Sprintf("input %d shares a file name with an earlier input and was saved as %s", i, filename))
		}
		content := art.AsBytes()
		if v := validateInput(filename, content, concat[i]); v != nil {
			logRejection(tc, "ffmpeg", v.rule)
			return sdk.Error(fmt.Sprintf("input %d is not allowed: %s", i, v.reason)), nil
		}
		path := filepath.Join(inputDir, filename)
		if err = os.WriteFile(path, content, 0o644); err != nil {
			return sdk.Error(fmt.Sprintf("failed to write input artifact %d: %v", i, err)), nil
		}
		inputPaths[i] = path
	}

	outputPath := filepath.Join(outputDir, p.OutputFilename)
	args := withProtocolWhitelist(substitutePlaceholders(tokens, inputPaths, outputPath, outputDir))

	timeout := defaultFFmpegTimeout
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds > 0 {
		timeout = *p.TimeoutSeconds
	}

	_ = tc.SendStatus("Running FFmpeg command...")

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args = append([]string{"-y"}, args...)

	execCmd := exec.CommandContext(timeoutCtx, "ffmpeg", args...)
	execCmd.Dir = inputDir
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
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}

		return sdk.Error(
			fmt.Sprintf("FFmpeg command failed (exit code %d)", exitCode),
			sdk.WithData(map[string]any{
				"command":        displayCommand(append([]string{"ffmpeg"}, args...)),
				"stderr":         errMsg,
				"stdout":         stdout.String(),
				"exit_code":      exitCode,
				"renamed_inputs": renamed,
			}),
		), nil
	}

	outputFiles, err := collectOutputFiles(outputDir)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to read output files: %v", err)), nil
	}

	if len(outputFiles) == 0 {
		msg := withRenamedInputs("FFmpeg command completed successfully but produced no output files.", renamed)
		if out := stdout.String(); out != "" {
			msg += "\n\nStdout:\n" + out
		}
		return sdk.OK(msg), nil
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
			Description: fmt.Sprintf("FFmpeg output: %s", filename),
		})
	}

	msg := withRenamedInputs(fmt.Sprintf("FFmpeg command completed successfully. Produced %d output file(s).", len(dataObjects)), renamed)
	if out := stdout.String(); out != "" {
		msg += "\n\nStdout:\n" + out
	}

	return sdk.OK(msg, sdk.WithDataObjects(dataObjects...)), nil
}

func withRenamedInputs(msg string, renamed []string) string {
	if len(renamed) == 0 {
		return msg
	}
	return msg + "\n\nNote: " + strings.Join(renamed, "; ") + "."
}

func executeFFprobe(ctx context.Context, p FFprobeParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return sdk.Error(ffmpegNotFound), nil
	}

	var extra []string
	if p.Command != nil && *p.Command != "" {
		var err error
		if extra, err = parseArgs(*p.Command, ffprobeQuotingExample); err != nil {
			return sdk.Error(fmt.Sprintf("invalid command: %v", err)), nil
		}
	}
	probed := append(slices.Clone(extra), "-i", "{input[0]}")
	if v := validateArgs(probed); v != nil {
		logRejection(tc, "ffprobe", v.rule)
		return sdk.Error(fmt.Sprintf("invalid command: %v", v)), nil
	}
	filename, _ := inputFilename(p.InputFile.Filename, 0, map[string]bool{})
	content := p.InputFile.AsBytes()
	if v := validateInput(filename, content, concatInputs(probed)[0]); v != nil {
		logRejection(tc, "ffprobe", v.rule)
		return sdk.Error(fmt.Sprintf("input_file is not allowed: %s", v.reason)), nil
	}

	workDir, err := os.MkdirTemp("", "ffprobe-*")
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create temp directory: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	inputPath := filepath.Join(workDir, filename)
	if err := os.WriteFile(inputPath, content, 0o644); err != nil {
		return sdk.Error(fmt.Sprintf("failed to write input artifact: %v", err)), nil
	}

	timeout := defaultFFprobeTimeout
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds > 0 {
		timeout = *p.TimeoutSeconds
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args := []string{"-v", "quiet", "-print_format", "json"}
	if len(extra) > 0 {
		args = append(args, extra...)
	} else {
		args = append(args, "-show_format", "-show_streams")
	}
	args = append(args, "-protocol_whitelist", protocolWhitelist, inputPath)

	execCmd := exec.CommandContext(timeoutCtx, "ffprobe", args...)
	execCmd.Dir = workDir
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
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}

		return sdk.Error(
			fmt.Sprintf("FFprobe command failed (exit code %d)", exitCode),
			sdk.WithData(map[string]any{
				"command":   displayCommand(append([]string{"ffprobe"}, args...)),
				"stderr":    errMsg,
				"exit_code": exitCode,
			}),
		), nil
	}

	output := stdout.String()
	if output == "" {
		return sdk.OK("FFprobe returned no output."), nil
	}

	return sdk.OK(
		"Media file metadata retrieved successfully.",
		sdk.WithData(map[string]any{
			"metadata": output,
		}),
	), nil
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
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".flv":
		return "video/x-flv"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".m4v":
		return "video/x-m4v"
	case ".ts":
		return "video/mp2t"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".aac":
		return "audio/aac"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".wma":
		return "audio/x-ms-wma"
	case ".opus":
		return "audio/opus"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".srt":
		return "application/x-subrip"
	case ".vtt":
		return "text/vtt"
	case ".ass":
		return "text/x-ssa"
	default:
		return "application/octet-stream"
	}
}
