// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package htmltopdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/chromium"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type stubRenderer struct {
	pdf       []byte
	err       error
	gotURL    string
	gotOpts   printOptions
	callCount int
}

func (s *stubRenderer) Render(_ context.Context, htmlFileURL string, opts printOptions) (renderOutput, error) {
	s.callCount++
	s.gotURL = htmlFileURL
	s.gotOpts = opts
	return renderOutput{pdf: s.pdf}, s.err
}

func withStub(t *testing.T, s pdfRenderer) {
	t.Helper()
	prev := defaultRenderer
	defaultRenderer = s
	t.Cleanup(func() { defaultRenderer = prev })
}

func TestExecute_RendersInputArtifact(t *testing.T) {
	stub := &stubRenderer{pdf: []byte("%PDF-1.7\nstub")}
	withStub(t, stub)

	res, err := executeConvert(t.Context(), Params{
		InputDocument:  sdk.Artifact{Filename: "in.html", Content: []byte("<!doctype html><html><body><h1>Hi</h1></body></html>")},
		OutputFilename: new("report"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success (%s)", res.Status, res.Message)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("want 1 data object, got %d", len(res.DataObjects))
	}
	obj := res.DataObjects[0]
	if obj.Name != "report.pdf" {
		t.Errorf("name = %q, want report.pdf", obj.Name)
	}
	if obj.MIMEType != "application/pdf" {
		t.Errorf("mime = %q, want application/pdf", obj.MIMEType)
	}
	if obj.Disposition != sdk.DispositionArtifact {
		t.Errorf("disposition = %q, want artifact", obj.Disposition)
	}
	if string(obj.Content) != "%PDF-1.7\nstub" {
		t.Errorf("content mismatch")
	}
	if !strings.HasPrefix(stub.gotURL, "file://") {
		t.Errorf("renderer got url %q, want file:// scheme", stub.gotURL)
	}
}

func TestExecute_DerivesNameFromArtifact(t *testing.T) {
	stub := &stubRenderer{pdf: []byte("pdf")}
	withStub(t, stub)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "memo.html", Content: []byte("<p>artifact</p>")},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success (%s)", res.Status, res.Message)
	}
	if got := res.DataObjects[0].Name; got != "memo.pdf" {
		t.Errorf("name = %q, want memo.pdf (derived from artifact)", got)
	}
}

func TestExecute_MissingInput(t *testing.T) {
	withStub(t, &stubRenderer{})
	res, _ := executeConvert(t.Context(), Params{InputDocument: sdk.Artifact{Content: []byte("   ")}}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Message, "input_document is required") {
		t.Errorf("message = %q, want the input_document-required guidance", res.Message)
	}
}

func TestExecute_ChromiumUnavailableReturnsCleanMessage(t *testing.T) {
	stub := &stubRenderer{err: &errChromiumNotFound{inner: fmt.Errorf("no Chromium binary found on PATH")}}
	withStub(t, stub)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "in.html", Content: []byte("<p>hi</p>")},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("handler returned a Go error; want a clean result error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Message, "Chromium is not available") {
		t.Errorf("message = %q, want the actionable Chromium-unavailable guidance", res.Message)
	}
}

func TestSchema_RequiresInputDocument(t *testing.T) {
	schema := sdk.BuildSchema[Params]()
	if !slices.Contains(schema.Required, "input_document") {
		t.Errorf("schema must mark input_document required, got required=%v", schema.Required)
	}
	if _, ok := schema.Properties["input_document"]; !ok {
		t.Error("schema is missing the input_document property")
	}
	if _, ok := schema.Properties["html_content"]; ok {
		t.Error("schema still exposes html_content; it must be removed")
	}
}

func TestExecute_RealChromium(t *testing.T) {
	if _, err := chromium.Find(); err != nil {
		t.Skipf("no Chromium available: %v", err)
	}

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "report.html", Content: []byte("<!doctype html><html><head><style>body{font-family:sans-serif}h1{color:#1a73e8}</style></head><body><h1>SAM Report</h1><p>Rendered by Chromium.</p></body></html>")},
		PageSize:      new("a4"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, want success (%s)", res.Status, res.Message)
	}
	content := res.DataObjects[0].Content
	if !strings.HasPrefix(string(content), "%PDF-") {
		t.Errorf("output does not start with the PDF magic header (%q)", string(content[:min(8, len(content))]))
	}
}

func TestBrowserHome(t *testing.T) {
	home, err := browserHome()
	if err != nil {
		t.Fatalf("browserHome: %v", err)
	}
	for _, sub := range []string{"", ".config", ".cache"} {
		info, statErr := os.Stat(filepath.Join(home, sub))
		if statErr != nil || !info.IsDir() {
			t.Fatalf("expected directory %q under HOME, got err=%v", sub, statErr)
		}
	}
	probe := filepath.Join(home, ".writetest")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		t.Fatalf("HOME is not writable: %v", err)
	}
	_ = os.Remove(probe)
}

func TestResolvePrintOptions(t *testing.T) {
	tests := []struct {
		name       string
		params     Params
		wantErr    bool
		wantW      float64
		wantH      float64
		wantLscape bool
		wantBg     bool
	}{
		{name: "defaults to letter portrait, background on", params: Params{}, wantW: 8.5, wantH: 11, wantBg: true},
		{name: "a4 landscape", params: Params{PageSize: new("A4"), Orientation: new("landscape")}, wantW: 8.27, wantH: 11.69, wantLscape: true, wantBg: true},
		{name: "background off", params: Params{PrintBackground: new(false)}, wantW: 8.5, wantH: 11, wantBg: false},
		{name: "bad page size", params: Params{PageSize: new("foolscap")}, wantErr: true},
		{name: "bad orientation", params: Params{Orientation: new("sideways")}, wantErr: true},
		{name: "negative margin", params: Params{MarginsInches: new(-1.0)}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := resolvePrintOptions(tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.paperWidth != tt.wantW || opts.paperHeight != tt.wantH {
				t.Errorf("paper = %vx%v, want %vx%v", opts.paperWidth, opts.paperHeight, tt.wantW, tt.wantH)
			}
			if opts.landscape != tt.wantLscape {
				t.Errorf("landscape = %v, want %v", opts.landscape, tt.wantLscape)
			}
			if opts.printBackground != tt.wantBg {
				t.Errorf("printBackground = %v, want %v", opts.printBackground, tt.wantBg)
			}
		})
	}
}

func TestFinalFilename(t *testing.T) {
	tests := []struct {
		requested string
		input     string
		want      string
	}{
		{"report.pdf", "", "report.pdf"},
		{"report", "", "report.pdf"},
		{"", "memo.html", "memo.pdf"},
		{"", "", "document.pdf"},
		{"", "/tmp/path/to/doc.htm", "doc.pdf"},
	}
	for _, tt := range tests {
		if got := finalFilename(tt.requested, tt.input); got != tt.want {
			t.Errorf("finalFilename(%q, %q) = %q, want %q", tt.requested, tt.input, got, tt.want)
		}
	}
}

type stagedFileRenderer struct {
	name string
	got  []byte
}

func (s *stagedFileRenderer) Render(_ context.Context, htmlFileURL string, _ printOptions) (renderOutput, error) {
	s.got, _ = os.ReadFile(filepath.Join(filepath.Dir(strings.TrimPrefix(htmlFileURL, "file://")), s.name))
	return renderOutput{pdf: []byte("%PDF-1.7\nstub")}, nil
}

func TestExecute_StagesImagesNextToTheHTML(t *testing.T) {
	stub := &stagedFileRenderer{name: "chart.png"}
	withStub(t, stub)

	res, err := executeConvert(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "report.html", Content: []byte(`<img src="chart.png">`)},
		Images:        []sdk.Artifact{{Filename: "charts/../chart.png", Content: []byte("png-bytes")}},
	}, &sdk.ToolContext{})
	if err != nil || res.Status != sdk.StatusSuccess {
		t.Fatalf("err=%v status=%q (%s)", err, res.Status, res.Message)
	}
	if string(stub.got) != "png-bytes" {
		t.Errorf("chart.png beside the HTML = %q, want the image artifact's bytes", stub.got)
	}
}

func TestExecute_RealChromiumEmbedsAStagedImage(t *testing.T) {
	if _, err := chromium.Find(); err != nil {
		t.Skipf("no Chromium available: %v", err)
	}
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0, 0x90, 0x77, 0x53, 0xde,
		0, 0, 0, 0x0c, 'I', 'D', 'A', 'T', 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0, 0, 0x03, 0x01, 0x01, 0, 0x18, 0xdd, 0x8d, 0xb0,
		0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82}

	render := func(images []sdk.Artifact) []byte {
		res, err := executeConvert(t.Context(), Params{
			InputDocument: sdk.Artifact{Filename: "report.html", Content: []byte(`<!doctype html><html><body><img src="dot.png" width="200" height="200"></body></html>`)},
			Images:        images,
		}, &sdk.ToolContext{})
		if err != nil || res.Status != sdk.StatusSuccess {
			t.Fatalf("err=%v status=%q (%s)", err, res.Status, res.Message)
		}
		return res.DataObjects[0].Content
	}

	pixelImage := regexp.MustCompile(`/Subtype /Image\s+/Width 1\s+/Height 1\b`)
	if !pixelImage.Match(render([]sdk.Artifact{{Filename: "dot.png", Content: png}})) {
		t.Error("the PDF has no 1x1 image; the staged dot.png did not load")
	}
	if pixelImage.Match(render(nil)) {
		t.Error("the PDF has a 1x1 image without dot.png staged; the check cannot tell the cases apart")
	}
}
