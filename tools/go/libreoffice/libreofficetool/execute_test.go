// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package libreofficetool

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const fakeSofficeScript = `#!/bin/sh
printf '%s\0' "$@" > "$FAKE_ARGV_FILE"
if [ -n "$FAKE_EXIT_CODE" ]; then
  echo "Unspecified Application Error" >&2
  exit "$FAKE_EXIT_CODE"
fi
if [ -n "$FAKE_KILLED" ]; then
  echo "Unspecified Application Error" >&2
  kill -9 $$
fi
if [ -n "$FAKE_CHILD" ]; then
  sleep 30 &
  echo $! > "$FAKE_CHILD_PID_FILE"
  if [ -n "$FAKE_HANG" ]; then
    wait
  fi
fi
prev=""
for arg; do
  case "$prev" in
    --outdir) outdir="$arg" ;;
    --convert-to) convert="$arg" ;;
  esac
  prev="$arg"
  input="$arg"
done
if [ -n "$FAKE_NO_OUTPUT_ERROR" ]; then
  echo "convert $input -> $outdir/x using filter : y"
  echo "$FAKE_NO_OUTPUT_ERROR" >&2
  exit 0
fi
base=$(basename "$input")
cat "$input" > "$outdir/${base%.*}.${convert%%:*}"
`

func useFakeSoffice(t *testing.T) (argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake soffice is a POSIX shell script")
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "soffice"), []byte(fakeSofficeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argvFile = filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_ARGV_FILE", argvFile)
	t.Setenv("FAKE_NO_OUTPUT_ERROR", "")
	t.Setenv("FAKE_EXIT_CODE", "")
	t.Setenv("FAKE_KILLED", "")
	t.Setenv("FAKE_CHILD", "")
	t.Setenv("FAKE_HANG", "")
	return argvFile
}

func startFakeSofficeChild(t *testing.T) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("FAKE_CHILD", "1")
	t.Setenv("FAKE_CHILD_PID_FILE", pidFile)
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			return
		}
		if child, err := os.FindProcess(pid); err == nil {
			_ = child.Kill()
		}
	})
}

const childOutlivesTheCallBound = 10 * time.Second

func recordedArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the fake soffice was not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

const crmTableHTML = `<!doctype html><html><body><table>
<tr><th>Name</th><th>Account</th></tr><tr><td>Alice</td><td>Acme</td></tr>
</table></body></html>`

func TestExecuteConvert_HTMLToSpreadsheetIsImportedByCalc(t *testing.T) {
	argvFile := useFakeSoffice(t)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "crm.html", Content: []byte(crmTableHTML)},
		TargetFormat:  "xlsx",
		Filter:        new("Calc MS Excel 2007 XML"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}

	argv := recordedArgv(t, argvFile)
	infilter := slices.Index(argv, "--infilter="+htmlCalcImportFilter)
	convertTo := slices.Index(argv, "--convert-to")
	if infilter < 0 || convertTo < 0 || infilter > convertTo {
		t.Errorf("argv = %q, want --infilter=%s before --convert-to", argv, htmlCalcImportFilter)
	}
	if convertTo >= 0 && argv[convertTo+1] != "xlsx:Calc MS Excel 2007 XML" {
		t.Errorf("--convert-to %q, want the caller's target and export filter unchanged", argv[convertTo+1])
	}
	if len(res.DataObjects) != 1 || res.DataObjects[0].Name != "crm.xlsx" || !bytes.Equal(res.DataObjects[0].Content, []byte(crmTableHTML)) {
		t.Errorf("data objects = %+v, want one artifact named crm.xlsx converted from the input", res.DataObjects)
	}
}

func TestExecuteConvert_OtherConversionsKeepTheDefaultImport(t *testing.T) {
	argvFile := useFakeSoffice(t)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "notes.html", Content: []byte("<p>hi</p>")},
		TargetFormat:  "pdf",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	for _, arg := range recordedArgv(t, argvFile) {
		if strings.HasPrefix(arg, "--infilter") {
			t.Errorf("HTML to PDF passed %q; Writer's default HTML import should be kept", arg)
		}
	}
}

const noExportFilterLine = "Error: no export filter for /w/out/x found, aborting."

func TestExecuteConvert_ReportsAMissingExportFilter(t *testing.T) {
	useFakeSoffice(t)
	t.Setenv("FAKE_NO_OUTPUT_ERROR", noExportFilterLine)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "notes.txt", Content: []byte("hi")},
		TargetFormat:  "xlsx",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "no export filter") {
		t.Errorf("status = %q, message = %q, want an error naming the missing export filter", res.Status, res.Message)
	}
	if detail, _ := res.Data["detail"].(string); !strings.HasPrefix(detail, "Error: no export filter for ") {
		t.Errorf("detail = %q, want soffice's own error line", detail)
	}
}

func TestExecuteConvert_CalcFilterInlinedInTargetFormat(t *testing.T) {
	argvFile := useFakeSoffice(t)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "crm.html", Content: []byte(crmTableHTML)},
		TargetFormat:  "pdf:calc_pdf_Export",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if !slices.Contains(recordedArgv(t, argvFile), "--infilter="+htmlCalcImportFilter) {
		t.Error("a Calc export filter given inside target_format did not get the Calc HTML import")
	}
}

func TestExecuteConvert_TimesOutWhileAChildHoldsTheOutput(t *testing.T) {
	useFakeSoffice(t)
	startFakeSofficeChild(t)
	t.Setenv("FAKE_HANG", "1")

	start := time.Now()
	res, err := executeConvert(t.Context(), Params{
		InputDocument:  sdk.Artifact{Filename: "notes.html", Content: []byte("<p>hi</p>")},
		TargetFormat:   "pdf",
		TimeoutSeconds: new(1),
	}, &sdk.ToolContext{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "timed out after 1s") {
		t.Errorf("status = %q, message = %q, want a timeout error", res.Status, res.Message)
	}
	if elapsed > childOutlivesTheCallBound {
		t.Errorf("returned after %s; the child holding soffice's output kept the call alive past the timeout", elapsed)
	}
}

func TestExecuteConvert_SucceedsWhileAChildHoldsTheOutput(t *testing.T) {
	useFakeSoffice(t)
	startFakeSofficeChild(t)

	start := time.Now()
	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "notes.html", Content: []byte("<p>hi</p>")},
		TargetFormat:  "pdf",
	}, &sdk.ToolContext{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess || len(res.DataObjects) != 1 {
		t.Fatalf("status = %q, message = %q, data objects = %d, want the converted file", res.Status, res.Message, len(res.DataObjects))
	}
	if elapsed > childOutlivesTheCallBound {
		t.Errorf("returned after %s; the call waited for a child that outlived soffice", elapsed)
	}
}

func TestExecuteConvert_ReportsCallerCancellation(t *testing.T) {
	const stopAfter = 500 * time.Millisecond
	for _, tc := range []struct {
		name        string
		callerCtx   func(context.Context) (context.Context, context.CancelFunc)
		wantMessage string
	}{
		{
			name: "cancelled",
			callerCtx: func(parent context.Context) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				time.AfterFunc(stopAfter, cancel)
				return ctx, cancel
			},
			wantMessage: "stopped before it finished: context canceled",
		},
		{
			name: "caller deadline shorter than the tool's timeout",
			callerCtx: func(parent context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(parent, stopAfter)
			},
			wantMessage: "stopped before it finished: context deadline exceeded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useFakeSoffice(t)
			startFakeSofficeChild(t)
			t.Setenv("FAKE_HANG", "1")

			ctx, cancel := tc.callerCtx(t.Context())
			defer cancel()
			start := time.Now()
			res, err := executeConvert(ctx, Params{
				InputDocument: sdk.Artifact{Filename: "notes.html", Content: []byte("<p>hi</p>")},
				TargetFormat:  "pdf",
			}, &sdk.ToolContext{})
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("executeConvert: %v", err)
			}
			if res.Status != sdk.StatusError || !strings.Contains(res.Message, tc.wantMessage) {
				t.Errorf("status = %q, message = %q, want %q, not the tool's own timeout", res.Status, res.Message, tc.wantMessage)
			}
			if elapsed > childOutlivesTheCallBound {
				t.Errorf("returned after %s; the call waited for a child that outlived the cancellation", elapsed)
			}
		})
	}
}

func TestExecuteConvert_ReportsSofficeKilledBySignal(t *testing.T) {
	useFakeSoffice(t)
	t.Setenv("FAKE_KILLED", "1")

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "crm.html", Content: []byte(crmTableHTML)},
		TargetFormat:  "xlsx",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "signal: killed") || !strings.Contains(res.Message, "Unspecified Application Error") {
		t.Errorf("status = %q, message = %q, want the signal and soffice's stderr", res.Status, res.Message)
	}
	if signal, _ := res.Data["signal"].(string); signal != "signal: killed" {
		t.Errorf("Data[signal] = %v, want %q", res.Data["signal"], "signal: killed")
	}
}

func TestExecuteConvert_ReportsSofficeExitCode(t *testing.T) {
	useFakeSoffice(t)
	t.Setenv("FAKE_EXIT_CODE", "134")

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "crm.html", Content: []byte(crmTableHTML)},
		TargetFormat:  "xlsx",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "exit code 134") || !strings.Contains(res.Message, "Unspecified Application Error") {
		t.Errorf("status = %q, message = %q, want the exit code and soffice's stderr", res.Status, res.Message)
	}
	if code, _ := res.Data["exit_code"].(int); code != 134 {
		t.Errorf("Data[exit_code] = %v, want 134", res.Data["exit_code"])
	}
}

func TestExecuteConvert_InputNamedLikeAWorkFileIsConvertedIntact(t *testing.T) {
	useFakeSoffice(t)

	const content = "real input"
	for _, name := range []string{"soffice.stdout", "soffice.stderr", "../soffice.stdout", "../../soffice.stderr"} {
		res, err := executeConvert(t.Context(), Params{
			InputDocument: sdk.Artifact{Filename: name, Content: []byte(content)},
			TargetFormat:  "txt",
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("executeConvert(%s): %v", name, err)
		}
		if res.Status != sdk.StatusSuccess || len(res.DataObjects) != 1 || string(res.DataObjects[0].Content) != content {
			t.Errorf("%s: status = %q, message = %q, data objects = %+v, want the input converted intact", name, res.Status, res.Message, res.DataObjects)
		}
	}
}

func TestExecuteConvert_InputStaysInsideTheWorkDir(t *testing.T) {
	useFakeSoffice(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	const escapee = "escape-probe.txt"
	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "../../" + escapee, Content: []byte("x")},
		TargetFormat:  "pdf",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess || len(res.DataObjects) != 1 || res.DataObjects[0].Name != "escape-probe.pdf" {
		t.Errorf("status = %q, message = %q, data objects = %+v, want the base name converted", res.Status, res.Message, res.DataObjects)
	}
	if _, err := os.Stat(filepath.Join(tmp, escapee)); !os.IsNotExist(err) {
		t.Errorf("a %q input was written outside the work dir (stat err = %v)", "../../"+escapee, err)
	}
	if leftovers, _ := os.ReadDir(tmp); len(leftovers) != 0 {
		t.Errorf("the temp dir holds %d entries after the call, want none", len(leftovers))
	}
}

func TestSafeInputName(t *testing.T) {
	for _, tc := range []struct {
		filename, want string
		wantErr        bool
	}{
		{"", "input", false},
		{"crm.html", "crm.html", false},
		{"../../crm.html", "crm.html", false},
		{"/etc/passwd", "passwd", false},
		{"dir/", "dir", false},
		{"..", "", true},
		{".", "", true},
		{"/", "", true},
		{"a\x00b", "", true},
	} {
		got, err := safeInputName(tc.filename)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("safeInputName(%q) = %q, %v; want %q, error %v", tc.filename, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestExecuteConvert_ReportsSofficeErrorWhenNothingIsProduced(t *testing.T) {
	useFakeSoffice(t)
	const line = "Error: source file could not be loaded"
	t.Setenv("FAKE_NO_OUTPUT_ERROR", line)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "notes.txt", Content: []byte("hi")},
		TargetFormat:  "xlsx",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, line) {
		t.Errorf("status = %q, message = %q, want soffice's error line in the message", res.Status, res.Message)
	}
}

func TestExecuteConvert_HTMLHintOnlyForHTMLWithoutAnHTMLExtension(t *testing.T) {
	useFakeSoffice(t)
	t.Setenv("FAKE_NO_OUTPUT_ERROR", noExportFilterLine)

	const hint = "ends in .html or .htm"
	for _, tc := range []struct {
		filename string
		wantHint bool
	}{
		{"report.docx", false},
		{"crm.html", false},
		{"crm.xhtml", true},
		{"input", true},
	} {
		res, err := executeConvert(t.Context(), Params{
			InputDocument: sdk.Artifact{Filename: tc.filename, Content: []byte(crmTableHTML)},
			TargetFormat:  "xlsx",
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("executeConvert(%s): %v", tc.filename, err)
		}
		if got := strings.Contains(res.Message, hint); got != tc.wantHint {
			t.Errorf("%s: message = %q, want the HTML extension hint = %v", tc.filename, res.Message, tc.wantHint)
		}
	}
}

func TestExecuteConvert_PointsHTMLToPDFAtHTMLToPDF(t *testing.T) {
	useFakeSoffice(t)
	t.Setenv("FAKE_NO_OUTPUT_ERROR", noExportFilterLine)

	for _, tc := range []struct {
		filename string
		format   string
		want     bool
	}{
		{"page.html", "pdf", true},
		{"page.htm", "pdf", true},
		{"page.html", "xlsx", false},
		{"report.docx", "pdf", false},
	} {
		res, err := executeConvert(t.Context(), Params{
			InputDocument: sdk.Artifact{Filename: tc.filename, Content: []byte(crmTableHTML)},
			TargetFormat:  tc.format,
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("executeConvert(%s): %v", tc.filename, err)
		}
		if got := strings.Contains(res.Message, "html_to_pdf"); got != tc.want {
			t.Errorf("%s to %s: message = %q, want html_to_pdf mentioned = %v", tc.filename, tc.format, res.Message, tc.want)
		}
	}
}

func TestExecuteConvert_HTMLTableToXLSX(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH")
	}

	res, err := executeConvert(t.Context(), Params{
		InputDocument:  sdk.Artifact{Filename: "crm.html", Content: []byte(crmTableHTML)},
		TargetFormat:   "xlsx",
		Filter:         new("Calc MS Excel 2007 XML"),
		TimeoutSeconds: new(90),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if len(res.DataObjects) != 1 || !bytes.HasPrefix(res.DataObjects[0].Content, []byte("PK")) {
		t.Errorf("data objects = %+v, want one XLSX (zip) artifact", res.DataObjects)
	}
}

func TestExecuteConvert_PDFToPPTX(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH")
	}
	convert := func(input sdk.Artifact, target string) sdk.DataObject {
		t.Helper()
		res, err := executeConvert(t.Context(), Params{InputDocument: input, TargetFormat: target, TimeoutSeconds: new(90)}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("executeConvert to %s: %v", target, err)
		}
		if res.Status != sdk.StatusSuccess || len(res.DataObjects) != 1 {
			t.Fatalf("convert to %s: status = %q, message = %q", target, res.Status, res.Message)
		}
		return res.DataObjects[0]
	}

	pdf := convert(sdk.Artifact{Filename: "deck.txt", Content: []byte("Quarterly review\n\nRevenue grew.\n")}, "pdf")
	pptx := convert(sdk.Artifact{Filename: pdf.Name, Content: pdf.Content}, "pptx")
	if pptx.Name != "deck.pptx" {
		t.Errorf("data object = %s, want deck.pptx", pptx.Name)
	}
	deck, err := zip.NewReader(bytes.NewReader(pptx.Content), int64(len(pptx.Content)))
	if err != nil {
		t.Fatalf("deck.pptx is not a zip: %v", err)
	}
	body, err := fs.ReadFile(deck, "ppt/slides/slide1.xml")
	if err != nil {
		t.Fatalf("deck.pptx has no readable first slide: %v", err)
	}
	if !bytes.Contains(body, []byte("Quarterly")) {
		t.Error("the first slide does not carry the PDF's text")
	}
}

func TestParamsSchemaRequiresOnlyDocumentAndFormat(t *testing.T) {
	got := sdk.BuildSchema[Params]().Required
	want := []string{"input_document", "target_format"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

func TestExecuteConvert_OmittedOptionalParamsUseDefaults(t *testing.T) {
	useFakeSoffice(t)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "notes.html", Content: []byte("<p>hi</p>")},
		TargetFormat:  "pdf",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if len(res.DataObjects) != 1 || res.DataObjects[0].Name != "notes.pdf" {
		t.Errorf("data objects = %+v, want one artifact named notes.pdf", res.DataObjects)
	}
}
