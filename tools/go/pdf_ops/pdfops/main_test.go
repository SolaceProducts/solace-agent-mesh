// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfops

import (
	"context"
	"fmt"
	"os"
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
	if pages < 1 {
		t.Fatalf("pages must be >= 1")
	}

	pagesJSON := "{"
	for i := 1; i <= pages; i++ {
		if i > 1 {
			pagesJSON += ","
		}
		pagesJSON += fmt.Sprintf(`"%d": { "content": { "text": [ { "value": "Page %d", "pos": [100, 700], "font": { "name": "$helv" } } ] } }`, i, i)
	}
	pagesJSON += "}"

	js := fmt.Sprintf(`{
  "paper": "A4P",
  "fonts": { "helv": { "name": "Helvetica", "size": 12 } },
  "pages": %s
}`, pagesJSON)

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

func artifact(name string, content []byte) sdk.Artifact {
	return sdk.Artifact{Filename: name, Content: content, MIMEType: "application/pdf"}
}

func pageCount(t *testing.T, data []byte) int {
	t.Helper()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "probe.pdf")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write probe pdf: %v", err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open probe pdf: %v", err)
	}
	defer func() { _ = f.Close() }()
	info, err := api.PDFInfo(t.Context(), f, p, nil, false, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("PDFInfo: %v", err)
	}
	return info.PageCount
}

func TestInvalidOperation(t *testing.T) {
	res, err := execute(context.Background(), Params{Operation: "does-not-exist"}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid operation") {
		t.Errorf("expected invalid operation error, got %+v", res)
	}
}

func TestMergeTwoPDFs(t *testing.T) {
	a := makeTestPDF(t, 2)
	b := makeTestPDF(t, 3)

	res, err := execute(context.Background(), Params{
		Operation:      opMerge,
		InputPDF:       sdk.Artifact{Filename: "a.pdf", Content: a, MIMEType: "application/pdf"},
		AdditionalPDFs: []sdk.Artifact{artifact("b.pdf", b)},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %+v", res)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 output, got %d", len(res.DataObjects))
	}
	if got := pageCount(t, res.DataObjects[0].Content); got != 5 {
		t.Errorf("merged PDF has %d pages, want 5", got)
	}
	if res.DataObjects[0].MIMEType != pdfMIMEType {
		t.Errorf("output mime = %q, want %q", res.DataObjects[0].MIMEType, pdfMIMEType)
	}
}

func TestMergeSingleInputFails(t *testing.T) {
	a := makeTestPDF(t, 1)
	res, err := execute(context.Background(), Params{
		Operation: opMerge,
		InputPDF:  sdk.Artifact{Filename: "a.pdf", Content: a, MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "at least two") {
		t.Errorf("expected error about >= 2 PDFs, got %+v", res)
	}
}

func TestSplitPreservesPageOrder(t *testing.T) {
	pdf := makeTestPDF(t, 12)

	res, err := execute(context.Background(), Params{
		Operation: opSplit,
		InputPDF:  sdk.Artifact{Filename: "src.pdf", Content: pdf, MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %+v", res)
	}
	if len(res.DataObjects) != 12 {
		t.Fatalf("expected 12 output pages, got %d", len(res.DataObjects))
	}
	for i, do := range res.DataObjects {
		want := fmt.Sprintf("page_%d.pdf", i+1)
		if do.Name != want {
			t.Errorf("output[%d] name = %q, want %q", i, do.Name, want)
		}
		if got := pageCount(t, do.Content); got != 1 {
			t.Errorf("output[%d] has %d pages, want 1", i, got)
		}
	}
}

func TestExtractPages(t *testing.T) {
	pdf := makeTestPDF(t, 5)

	res, err := execute(context.Background(), Params{
		Operation: opExtractPages,
		InputPDF:  sdk.Artifact{Filename: "src.pdf", Content: pdf, MIMEType: "application/pdf"},
		PageRange: new("2-3"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %+v", res)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 output, got %d", len(res.DataObjects))
	}
	if got := pageCount(t, res.DataObjects[0].Content); got != 2 {
		t.Errorf("extracted PDF has %d pages, want 2", got)
	}
}

func TestExtractPagesRequiresRange(t *testing.T) {
	pdf := makeTestPDF(t, 2)
	res, err := execute(context.Background(), Params{
		Operation: opExtractPages,
		InputPDF:  sdk.Artifact{Filename: "src.pdf", Content: pdf, MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "page_range") {
		t.Errorf("expected error about page_range, got %+v", res)
	}
}

func TestInfo(t *testing.T) {
	pdf := makeTestPDF(t, 4)
	res, err := execute(context.Background(), Params{
		Operation: opInfo,
		InputPDF:  sdk.Artifact{Filename: "src.pdf", Content: pdf, MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %+v", res)
	}
	if res.Data == nil {
		t.Fatalf("expected Data to be populated")
	}
	if pc, ok := res.Data["page_count"].(int); !ok || pc != 4 {
		t.Errorf("page_count = %v (type %T), want 4", res.Data["page_count"], res.Data["page_count"])
	}
	if !strings.Contains(res.Message, "page_count") {
		t.Errorf("info message missing page_count: %q", res.Message)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("info should not return artifacts, got %d", len(res.DataObjects))
	}
}

func TestRotateValidation(t *testing.T) {
	pdf := makeTestPDF(t, 1)

	bad := 45
	res, err := execute(context.Background(), Params{
		Operation: opRotate,
		InputPDF:  sdk.Artifact{Filename: "src.pdf", Content: pdf, MIMEType: "application/pdf"},
		Rotation:  &bad,
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid rotation") {
		t.Errorf("expected rotation validation error, got %+v", res)
	}

	res2, _ := execute(context.Background(), Params{
		Operation: opRotate,
		InputPDF:  sdk.Artifact{Filename: "src.pdf", Content: pdf, MIMEType: "application/pdf"},
	}, &sdk.ToolContext{})
	if res2.Status != sdk.StatusError || !strings.Contains(res2.Message, "rotation is required") {
		t.Errorf("expected rotation-required error, got %+v", res2)
	}
}

func TestMissingInput(t *testing.T) {
	res, _ := execute(context.Background(), Params{Operation: opInfo}, &sdk.ToolContext{})
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "input_pdf") {
		t.Errorf("expected input_pdf error, got %+v", res)
	}
}

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"foo.pdf":     "foo.pdf",
		"../a.pdf":    "a.pdf",
		"/etc/passwd": "passwd",
		"a/b/c.pdf":   "c.pdf",
		"":            ".",
	}
	for in, want := range cases {
		got := safeFilename(in)
		if got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParamsSchemaRequiresOperationAndInputPDF(t *testing.T) {
	got := sdk.BuildSchema[Params]().Required
	want := []string{"operation", "input_pdf"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

func TestWatermarkWithOnlyTextUsesDefaults(t *testing.T) {
	res, err := execute(t.Context(), Params{
		Operation:     opWatermark,
		InputPDF:      sdk.Artifact{Filename: "src.pdf", Content: makeTestPDF(t, 2), MIMEType: "application/pdf"},
		WatermarkText: new("DRAFT"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %+v", res)
	}
	if len(res.DataObjects) != 1 || res.DataObjects[0].Name != "watermarked.pdf" {
		t.Errorf("data objects = %+v, want one artifact named watermarked.pdf", res.DataObjects)
	}
}
