// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rendertoimages

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestMain(m *testing.M) {
	api.DisableConfigDir() //nolint:staticcheck // SA1019: LoadConfiguration migration needs per-call config; separate change
	os.Exit(m.Run())
}

func makeTestPDF(t *testing.T, pages int) []byte {
	t.Helper()
	pagesJSON := "{"
	for i := 1; i <= pages; i++ {
		if i > 1 {
			pagesJSON += ","
		}
		pagesJSON += fmt.Sprintf(`"%d": { "content": { "text": [ { "value": "Page %d", "pos": [100, 700], "font": { "name": "$helv" } } ] } }`, i, i)
	}
	pagesJSON += "}"
	js := fmt.Sprintf(`{ "paper": "A4P", "fonts": { "helv": { "name": "Helvetica", "size": 12 } }, "pages": %s }`, pagesJSON)

	tmp := t.TempDir()
	jsPath := filepath.Join(tmp, "src.json")
	pdfPath := filepath.Join(tmp, "src.pdf")
	if err := os.WriteFile(jsPath, []byte(js), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	if err := api.CreateFile(t.Context(), "", jsPath, pdfPath, model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	data, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("read pdf: %v", err)
	}
	return data
}

func requireRasterizer(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pdftoppm"); err == nil {
		return
	}
	if _, err := exec.LookPath("convert"); err == nil {
		return
	}
	if _, err := exec.LookPath("magick"); err == nil {
		return
	}
	t.Skip("no PDF rasterizer (pdftoppm/convert/magick) on PATH")
}

func pngObjects(res *sdk.Result) []sdk.DataObject {
	var out []sdk.DataObject
	for _, do := range res.DataObjects {
		if do.MIMEType == pngMIMEType {
			out = append(out, do)
		}
	}
	return out
}

func TestRender_MissingInput(t *testing.T) {
	res, err := executeRender(t.Context(), Params{}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "input_document is required") {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
}

func TestRender_InvalidPages(t *testing.T) {
	pdf := makeTestPDF(t, 2)
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "deck.pdf", Content: pdf, MIMEType: "application/pdf"},
		Pages:         new("abc"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid pages") {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
}

func TestRender_PDFPassthrough_AllPages(t *testing.T) {
	requireRasterizer(t)
	pdf := makeTestPDF(t, 3)
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "deck.pdf", Content: pdf, MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
	objs := pngObjects(res)
	if len(objs) != 3 {
		t.Fatalf("got %d page images, want 3", len(objs))
	}
	for i, do := range objs {
		want := fmt.Sprintf("deck-page-%d.png", i+1)
		if do.Name != want {
			t.Errorf("page %d name = %q, want %q", i+1, do.Name, want)
		}
		if do.Disposition != sdk.DispositionArtifact {
			t.Errorf("page %d disposition = %q, want artifact", i+1, do.Disposition)
		}
		if len(do.Content) == 0 {
			t.Errorf("page %d has empty content", i+1)
		}
	}
	if !strings.Contains(res.Message, "load_artifact") {
		t.Errorf("result message should point the model at load_artifact: %q", res.Message)
	}
}

func TestRender_PageRange(t *testing.T) {
	requireRasterizer(t)
	pdf := makeTestPDF(t, 5)
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "deck.pdf", Content: pdf, MIMEType: "application/pdf"},
		Pages:         new("2-4"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
	objs := pngObjects(res)
	if len(objs) != 3 {
		t.Fatalf("got %d page images, want 3 (pages 2-4)", len(objs))
	}
	wantNames := []string{"deck-page-2.png", "deck-page-3.png", "deck-page-4.png"}
	for i, do := range objs {
		if do.Name != wantNames[i] {
			t.Errorf("object %d name = %q, want %q", i, do.Name, wantNames[i])
		}
	}
}

func TestRender_MaxPagesCap(t *testing.T) {
	requireRasterizer(t)
	pdf := makeTestPDF(t, 5)
	cap2 := 2
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "deck.pdf", Content: pdf, MIMEType: "application/pdf"},
		MaxPages:      &cap2,
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
	if objs := pngObjects(res); len(objs) != 2 {
		t.Fatalf("got %d page images, want 2 (capped)", len(objs))
	}
	if !strings.Contains(res.Message, "skipped") || !strings.Contains(res.Message, "max_pages") {
		t.Errorf("capped result should announce the truncation: %q", res.Message)
	}
}

func TestRender_RangeMatchesNothing(t *testing.T) {
	requireRasterizer(t)
	pdf := makeTestPDF(t, 2)
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "deck.pdf", Content: pdf, MIMEType: "application/pdf"},
		Pages:         new("9-10"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "past the end") {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
}

func TestRender_CorruptPDF(t *testing.T) {
	requireRasterizer(t)
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "deck.pdf", Content: []byte("not a real pdf"), MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("status=%v msg=%q, want error", res.Status, res.Message)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("corrupt input should produce no artifacts, got %d", len(res.DataObjects))
	}
}

func TestRender_HTMLViaSoffice(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not on PATH")
	}
	requireRasterizer(t)
	html := []byte(`<html><body><h1>Hello</h1><p>A simple page.</p></body></html>`)
	res, err := executeRender(t.Context(), Params{
		InputDocument: sdk.Artifact{Filename: "page.html", Content: html, MIMEType: "text/html"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status=%v msg=%q", res.Status, res.Message)
	}
	objs := pngObjects(res)
	if len(objs) < 1 {
		t.Fatalf("got %d page images, want >= 1", len(objs))
	}
	if objs[0].Name != "page-page-1.png" {
		t.Errorf("first page name = %q, want page-page-1.png", objs[0].Name)
	}
}

func TestParseRange(t *testing.T) {
	cases := []struct {
		in          string
		first, last int
		wantErr     bool
	}{
		{"", 1, 0, false},
		{"3", 3, 3, false},
		{"2-5", 2, 5, false},
		{"4-", 4, 0, false},
		{"  2-3 ", 2, 3, false},
		{"0", 0, 0, true},
		{"5-2", 0, 0, true},
		{"abc", 0, 0, true},
		{"-3", 0, 0, true},
	}
	for _, c := range cases {
		first, last, err := parseRange(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseRange(%q): want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRange(%q): unexpected err %v", c.in, err)
			continue
		}
		if first != c.first || last != c.last {
			t.Errorf("parseRange(%q) = (%d,%d), want (%d,%d)", c.in, first, last, c.first, c.last)
		}
	}
}

func TestTrailingInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"page-1.png", 1, true},
		{"page-07.png", 7, true},
		{"/tmp/out/page-12.png", 12, true},
		{"slide.png", 0, false},
	}
	for _, c := range cases {
		got, ok := trailingInt(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("trailingInt(%q) = (%d,%v), want (%d,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsPDF(t *testing.T) {
	if !isPDF("a.pdf", "") {
		t.Error("extension .pdf should be PDF")
	}
	if !isPDF("a.PDF", "") {
		t.Error("extension .PDF should be PDF (case-insensitive)")
	}
	if !isPDF("noext", "application/pdf") {
		t.Error("application/pdf MIME should be PDF")
	}
	if isPDF("a.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation") {
		t.Error("pptx should not be PDF")
	}
}

func TestCollectPNGs_NumericOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"page-10.png", "page-2.png", "page-1.png"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := collectPNGs(dir)
	if err != nil {
		t.Fatalf("collectPNGs: %v", err)
	}
	want := []string{"page-1.png", "page-2.png", "page-10.png"}
	for i, p := range paths {
		if filepath.Base(p) != want[i] {
			t.Errorf("position %d = %q, want %q", i, filepath.Base(p), want[i])
		}
	}
}

func TestParamsSchemaRequiresOnlyInputDocument(t *testing.T) {
	got := sdk.BuildSchema[Params]().Required
	want := []string{"input_document"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}
