// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package libreofficetool

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultTimeout  = 120
	maxOutputBytes  = 200 * 1024 * 1024
	sofficeNotFound = "LibreOffice is required for document conversion but was not found on this host. Install it — Windows: winget install TheDocumentFoundation.LibreOffice; macOS: brew install --cask libreoffice; Linux: apt-get install libreoffice. The soffice binary was not found on PATH."
)

var targetFormatPattern = regexp.MustCompile(`^[a-z0-9]{2,8}(:[A-Za-z0-9_ .,=:\-()]+)?$`)

const htmlCalcImportFilter = "HTML (StarCalc)"

var spreadsheetFormats = map[string]bool{
	"csv": true, "fods": true, "ods": true, "ots": true,
	"xls": true, "xlsm": true, "xlsx": true, "xlt": true, "xltx": true,
}

const pdfImpressImportFilter = "impress_pdf_import"

var presentationFormats = map[string]bool{
	"fodp": true, "odp": true, "otp": true,
	"pot": true, "potm": true, "potx": true, "pps": true, "ppsx": true, "ppt": true, "pptm": true, "pptx": true,
}

func importFilterArgs(inputName, formatExt, exportFilter string) []string {
	switch strings.ToLower(filepath.Ext(inputName)) {
	case ".html", ".htm":
		if targetsFamily(spreadsheetFormats, isCalcExportFilter, formatExt, exportFilter) {
			return []string{"--infilter=" + htmlCalcImportFilter}
		}
	case ".pdf":
		if targetsFamily(presentationFormats, isImpressExportFilter, formatExt, exportFilter) {
			return []string{"--infilter=" + pdfImpressImportFilter}
		}
	}
	return nil
}

func targetsFamily(formats map[string]bool, isFamilyFilter func(string) bool, formatExt, exportFilter string) bool {
	if exportFilter != "" {
		return isFamilyFilter(exportFilter)
	}
	return formats[formatExt]
}

func isImpressExportFilter(filter string) bool {
	name, _, _ := strings.Cut(strings.ToLower(filter), ":")
	return strings.Contains(name, "impress") || strings.Contains(name, "powerpoint") ||
		strings.Contains(name, "presentation")
}

var calcExportFilterNames = map[string]bool{"dbase": true, "dif": true, "sylk": true}

func isCalcExportFilter(filter string) bool {
	name, _, _ := strings.Cut(strings.ToLower(filter), ":")
	return strings.Contains(name, "calc") || strings.Contains(name, "excel") ||
		strings.Contains(name, "spreadsheet") || calcExportFilterNames[strings.TrimSpace(name)]
}

const htmlToPDFHint = " For HTML to PDF use the html_to_pdf tool if it is available to you."

func isHTMLInput(inputName string) bool {
	switch strings.ToLower(filepath.Ext(inputName)) {
	case ".html", ".htm", ".xhtml", ".xht":
		return true
	}
	return false
}

func htmlNeedsHTMLExtension(inputName string) bool {
	switch strings.ToLower(filepath.Ext(inputName)) {
	case "", ".xhtml", ".xht":
		return true
	}
	return false
}

func captureOutputInFiles(cmd *exec.Cmd, dir string) (readOutput func() (stdout, stderr string), err error) {
	stdoutFile, err := os.Create(filepath.Join(dir, "soffice.stdout"))
	if err != nil {
		return nil, fmt.Errorf("create stdout file: %w", err)
	}
	stderrFile, err := os.Create(filepath.Join(dir, "soffice.stderr"))
	if err != nil {
		_ = stdoutFile.Close()
		return nil, fmt.Errorf("create stderr file: %w", err)
	}
	cmd.Stdout, cmd.Stderr = stdoutFile, stderrFile
	return func() (string, string) {
		return readAndClose(stdoutFile), readAndClose(stderrFile)
	}, nil
}

func readAndClose(f *os.File) string {
	defer func() { _ = f.Close() }()
	content, err := os.ReadFile(f.Name())
	if err != nil {
		return fmt.Sprintf("(could not read %s: %v)", filepath.Base(f.Name()), err)
	}
	return string(content)
}

func safeInputName(filename string) (string, error) {
	if filename == "" {
		return "input", nil
	}
	base := filepath.Base(filename)
	switch base {
	case ".", "..", string(filepath.Separator):
		return "", fmt.Errorf("invalid input_document filename %q: it does not name a file", filename)
	}
	if strings.ContainsRune(base, 0) {
		return "", fmt.Errorf("invalid input_document filename %q: it contains a NUL byte", filename)
	}
	return base, nil
}

type sofficeRun struct {
	command        []string
	stdout, stderr string
	err            error
	state          *os.ProcessState
	killedOnCancel bool
}

func (r sofficeRun) succeeded() bool {
	return r.err == nil || (r.state != nil && r.state.Success())
}

func (r sofficeRun) stoppedByContext() bool {
	if r.state == nil {
		return errors.Is(r.err, context.Canceled) || errors.Is(r.err, context.DeadlineExceeded)
	}
	if !r.killedOnCancel {
		return false
	}
	return !r.state.Exited() || runtime.GOOS == "windows"
}

func (r sofficeRun) failure(callerCtx context.Context, timeout int) *sdk.Result {
	output := map[string]any{"stderr": r.stderr, "stdout": r.stdout}
	if r.stoppedByContext() {
		if callerCtx.Err() != nil {
			return sdk.Error(fmt.Sprintf("LibreOffice conversion was stopped before it finished: %v", callerCtx.Err()), sdk.WithData(output))
		}
		return sdk.Error(fmt.Sprintf("LibreOffice conversion timed out after %ds", timeout), sdk.WithData(output))
	}
	output["command"] = r.command
	if r.state == nil {
		return sdk.Error(fmt.Sprintf("could not run LibreOffice: %v", r.err), sdk.WithData(output))
	}
	detail := cmp.Or(strings.TrimSpace(r.stderr), r.err.Error())
	exitCode := r.state.ExitCode()
	output["exit_code"] = exitCode
	if !r.state.Exited() {
		output["signal"] = r.state.String()
		return sdk.Error(fmt.Sprintf("LibreOffice conversion failed (%s): %s", r.state, detail), sdk.WithData(output))
	}
	return sdk.Error(fmt.Sprintf("LibreOffice conversion failed (exit code %d): %s", exitCode, detail), sdk.WithData(output))
}

func missingExportFilter(stderr string) string {
	return stderrLineWithPrefix(stderr, "Error: no export filter")
}

const sofficeErrorPrefix = "Error:"

func stderrLineWithPrefix(stderr, prefix string) string {
	for line := range strings.SplitSeq(stderr, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

const instructions = `This tool converts a single input document between formats using headless LibreOffice.

## Input/Output
- Provide the input document as an artifact via the ` + "`input_document`" + ` parameter.
- Set ` + "`target_format`" + ` to the lowercase file extension (no dot) of the desired output: ` + "`pdf`" + `, ` + "`docx`" + `, ` + "`odt`" + `, ` + "`pptx`" + `, ` + "`xlsx`" + `, ` + "`csv`" + `, ` + "`html`" + `, ` + "`txt`" + `, ` + "`epub`" + `, ` + "`png`" + `, ` + "`svg`" + `, ` + "`rtf`" + `, etc.
- Optional ` + "`output_filename`" + ` overrides the generated filename (default ` + "`<input-basename>.<target_format>`" + `).
- Optional ` + "`filter`" + ` passes a LibreOffice export filter string (e.g. ` + "`Text (encoded):UTF8`" + `) for advanced export control. Appended as ` + "`target_format:filter`" + `.
- Optional ` + "`timeout_seconds`" + ` overrides the default 120s conversion timeout.

## Limitations
- Exactly one input and one output artifact per call. Use separate calls for batch work.
- Output size cap: 200 MiB per file. If a conversion produces a larger file the tool returns an error.
- The host must have ` + "`soffice`" + ` on PATH. Otherwise the tool returns a clean error — no panic, no partial work.

## When to use
Use this tool for office-document and plain-text format conversions: any of Word/Excel/PowerPoint/OpenDocument to PDF, PDF to text/HTML, PDF to PPTX/ODP (one slide per page), HTML to DOCX, CSV to XLSX, EPUB export, and so on. LibreOffice has no export filter for HTML to PDF; use ` + "`html_to_pdf`" + ` for that if it is available to you. For PDF↔PDF operations (merge/split/extract pages/watermark/encrypt) use ` + "`pdf_ops`" + ` instead. For audio/video use ` + "`ffmpeg`" + `; for images use ` + "`image_magick`" + `.
`

type Params struct {
	InputDocument  sdk.Artifact `json:"input_document" desc:"The document to convert: a filename already saved in this session, never a path or an invented name."`
	TargetFormat   string       `json:"target_format" desc:"Target extension (lowercase, no dot): pdf, docx, odt, pptx, xlsx, csv, html, txt, epub, png, svg, rtf, etc."`
	OutputFilename *string      `json:"output_filename" desc:"Output filename. Defaults to <input-basename>.<target_format>."`
	Filter         *string      `json:"filter" desc:"Optional LibreOffice export filter string (e.g. 'Text (encoded):UTF8'). Appended as target_format:filter."`
	TimeoutSeconds *int         `json:"timeout_seconds" desc:"Maximum execution time in seconds. Default is 120."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("libreoffice_convert",
			"Converts documents between formats using headless LibreOffice — supports office documents (docx/odt/xlsx/pptx), PDF, HTML, CSV, plain text, EPUB, and more",
			executeConvert,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func executeConvert(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.InputDocument.Filename == "" && len(p.InputDocument.Content) == 0 {
		return sdk.Error("input_document is required"), nil
	}
	if p.TargetFormat == "" {
		return sdk.Error("target_format is required"), nil
	}
	if !targetFormatPattern.MatchString(p.TargetFormat) {
		return sdk.Error(fmt.Sprintf("invalid target_format %q: must match %s", p.TargetFormat, targetFormatPattern.String())), nil
	}
	if derefString(p.Filter) != "" {
		combined := p.TargetFormat + ":" + derefString(p.Filter)
		if !targetFormatPattern.MatchString(combined) {
			return sdk.Error(fmt.Sprintf("invalid filter %q: disallowed characters", derefString(p.Filter))), nil
		}
	}

	soffice, err := exec.LookPath("soffice")
	if err != nil {
		return sdk.Error(sofficeNotFound), nil
	}

	formatExt := p.TargetFormat
	if i := strings.Index(formatExt, ":"); i >= 0 {
		formatExt = formatExt[:i]
	}

	workDir, err := os.MkdirTemp("", "libreoffice-*")
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create temp directory: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	profileDir := filepath.Join(workDir, "profile")
	inputDir := filepath.Join(workDir, "in")
	outputDir := filepath.Join(workDir, "out")
	if err = os.MkdirAll(profileDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create profile directory: %v", err)), nil
	}
	if err = os.MkdirAll(inputDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create input directory: %v", err)), nil
	}
	if err = os.MkdirAll(outputDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create output directory: %v", err)), nil
	}

	inputName, err := safeInputName(p.InputDocument.Filename)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	inputPath := filepath.Join(inputDir, inputName)
	if err = os.WriteFile(inputPath, p.InputDocument.AsBytes(), 0o644); err != nil {
		return sdk.Error(fmt.Sprintf("failed to write input artifact: %v", err)), nil
	}

	convertTo := p.TargetFormat
	if derefString(p.Filter) != "" {
		convertTo = p.TargetFormat + ":" + derefString(p.Filter)
	}
	_, exportFilter, _ := strings.Cut(convertTo, ":")

	timeout := defaultTimeout
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds > 0 {
		timeout = *p.TimeoutSeconds
	}

	_ = tc.SendStatus(fmt.Sprintf("Converting %s to %s via LibreOffice...", inputName, formatExt))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	args := []string{
		"--headless",
		"--norestore",
		"--nologo",
		"--nofirststartwizard",
		"-env:UserInstallation=file://" + profileDir,
	}
	args = append(args, importFilterArgs(inputName, formatExt, exportFilter)...)
	args = append(args,
		"--convert-to", convertTo,
		"--outdir", outputDir,
		inputPath,
	)
	cmd := exec.CommandContext(timeoutCtx, soffice, args...)
	var killedOnCancel atomic.Bool
	cmd.Cancel = func() error {
		killedOnCancel.Store(true)
		return cmd.Process.Kill()
	}
	readOutput, err := captureOutputInFiles(cmd, workDir)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to capture LibreOffice output: %v", err)), nil
	}
	run := sofficeRun{command: cmd.Args, err: cmd.Run()}
	run.state = cmd.ProcessState
	run.killedOnCancel = killedOnCancel.Load()
	run.stdout, run.stderr = readOutput()
	if !run.succeeded() {
		return run.failure(ctx, timeout), nil
	}
	stdout, stderr := run.stdout, run.stderr

	inputBase := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	producedPath := filepath.Join(outputDir, inputBase+"."+formatExt)
	if _, statErr := os.Stat(producedPath); statErr != nil {
		entries, readErr := os.ReadDir(outputDir)
		if readErr != nil || len(entries) == 0 {
			if line := missingExportFilter(stderr); line != "" {
				msg := fmt.Sprintf("LibreOffice cannot convert %s to %s: it has no export filter for this kind of document. Choose a target format this document type can be saved as.",
					inputName, formatExt)
				if isHTMLInput(inputName) && formatExt == "pdf" {
					msg += htmlToPDFHint
				}
				if htmlNeedsHTMLExtension(inputName) {
					msg += " HTML converts to spreadsheet formats only when its filename ends in .html or .htm."
				}
				return sdk.Error(msg, sdk.WithData(map[string]any{
					"detail": line,
					"stderr": stderr,
					"stdout": stdout,
				})), nil
			}
			msg := "LibreOffice reported success but produced no output file"
			if line := stderrLineWithPrefix(stderr, sofficeErrorPrefix); line != "" {
				msg += ": " + line
			}
			return sdk.Error(msg, sdk.WithData(map[string]any{"stderr": stderr, "stdout": stdout})), nil
		}
		producedPath = filepath.Join(outputDir, entries[0].Name())
	}

	fi, err := os.Stat(producedPath)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to stat output file: %v", err)), nil
	}
	if fi.Size() > maxOutputBytes {
		return sdk.Error(fmt.Sprintf(
			"output file %s is %d bytes, exceeds the %d-byte (200 MiB) cap",
			filepath.Base(producedPath), fi.Size(), maxOutputBytes,
		)), nil
	}

	content, err := os.ReadFile(producedPath)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to read output file: %v", err)), nil
	}

	outName := derefString(p.OutputFilename)
	if outName == "" {
		outName = filepath.Base(producedPath)
	}

	mimeType := detectMIMEType(outName)

	msg := fmt.Sprintf("Converted %s to %s (%d bytes).", inputName, outName, len(content))
	return sdk.OK(msg, sdk.WithDataObjects(sdk.DataObject{
		Name:        outName,
		Content:     content,
		MIMEType:    mimeType,
		Disposition: sdk.DispositionArtifact,
		Description: fmt.Sprintf("LibreOffice conversion output (%s)", formatExt),
	})), nil
}

func detectMIMEType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return "application/octet-stream"
	}
	if mt := mime.TypeByExtension(ext); mt != "" {
		return mt
	}
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".ods":
		return "application/vnd.oasis.opendocument.spreadsheet"
	case ".odp":
		return "application/vnd.oasis.opendocument.presentation"
	case ".rtf":
		return "application/rtf"
	case ".epub":
		return "application/epub+zip"
	case ".html", ".htm":
		return "text/html"
	case ".csv":
		return "text/csv"
	case ".txt":
		return "text/plain"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
