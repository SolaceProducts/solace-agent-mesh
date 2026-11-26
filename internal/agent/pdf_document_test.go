// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func makeImagePDFDim(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := api.ImportImages(t.Context(), nil, &out, []io.Reader{bytes.NewReader(pngBytes(t, w, h))},
		pdfcpu.DefaultImportConfig(), model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("import images: %v", err)
	}
	return out.Bytes()
}

func makeImagePDF(t *testing.T) []byte { return makeImagePDFDim(t, 200, 200) }

func stampImageOnText(t *testing.T, dim int) []byte {
	t.Helper()
	wm, err := api.ImageWatermarkForReader(t.Context(), bytes.NewReader(pngBytes(t, dim, dim)), "scale:0.5 rel, pos:c", true, false, types.POINTS)
	if err != nil {
		t.Fatalf("build image watermark: %v", err)
	}
	var out bytes.Buffer
	if err := api.AddWatermarks(t.Context(), bytes.NewReader(readTextPDF(t)), &out, nil, wm, model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("stamp image: %v", err)
	}
	return out.Bytes()
}

func makeMixedPDF(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	srcs := []io.ReadSeeker{
		bytes.NewReader(readTextPDF(t)),
		bytes.NewReader(makeImagePDF(t)),
	}
	if err := api.MergeRaw(t.Context(), srcs, &out, false, model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("merge pdfs: %v", err)
	}
	return out.Bytes()
}

func readTextPDF(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/text_document.pdf")
	if err != nil {
		t.Fatalf("read text pdf fixture: %v", err)
	}
	return b
}

func TestProbePDFStructure(t *testing.T) {
	ctx := t.Context()

	if s, err := probePDFStructure(ctx, makeImagePDF(t)); err != nil || s.pageCount != 1 {
		t.Errorf("image PDF: pageCount=%d err=%v; want 1, nil", s.pageCount, err)
	}
	if s, err := probePDFStructure(ctx, makeMixedPDF(t)); err != nil || s.pageCount != 2 {
		t.Errorf("mixed PDF: pageCount=%d err=%v; want 2, nil", s.pageCount, err)
	}

	if s, err := probePDFStructure(ctx, makeImagePDFDim(t, 600, 600)); err != nil || !s.substantial {
		t.Errorf("600x600 image should be substantial (substantial=%v err=%v)", s.substantial, err)
	}
	if s, err := probePDFStructure(ctx, makeImagePDFDim(t, 200, 200)); err != nil || s.substantial {
		t.Errorf("200x200 logo should NOT be substantial (substantial=%v err=%v)", s.substantial, err)
	}
	if s, err := probePDFStructure(ctx, readTextPDF(t)); err != nil || s.substantial {
		t.Errorf("text-only PDF has no substantial image (substantial=%v err=%v)", s.substantial, err)
	}

	if s, err := probePDFStructure(ctx, stampImageOnText(t, 600)); err != nil || !s.substantial {
		t.Errorf("text page with a 600px figure should be substantial (substantial=%v err=%v)", s.substantial, err)
	}
	if s, err := probePDFStructure(ctx, stampImageOnText(t, 200)); err != nil || s.substantial {
		t.Errorf("text page with only a 200px logo should NOT be substantial (substantial=%v err=%v)", s.substantial, err)
	}

	if _, err := probePDFStructure(ctx, []byte("not a pdf")); err == nil {
		t.Error("corrupt input should return a parse error")
	}
}

func TestPDFHasSparseTextPage(t *testing.T) {
	ctx := t.Context()

	if sparse, err := pdfHasSparseTextPage(ctx, makeImagePDF(t), 1); err != nil || !sparse {
		t.Errorf("image-only PDF should have a sparse text page (sparse=%v err=%v)", sparse, err)
	}
	if sparse, err := pdfHasSparseTextPage(ctx, readTextPDF(t), 1); err != nil || sparse {
		t.Errorf("text-rich PDF should NOT have a sparse text page (sparse=%v err=%v)", sparse, err)
	}
	if sparse, err := pdfHasSparseTextPage(ctx, makeMixedPDF(t), 2); err != nil || !sparse {
		t.Errorf("mixed PDF should have a sparse text page (sparse=%v err=%v)", sparse, err)
	}
	if sparse, err := pdfHasSparseTextPage(ctx, readTextPDF(t), 0); err != nil || sparse {
		t.Errorf("text PDF with pageCount=0 should clamp to text-rich (sparse=%v err=%v)", sparse, err)
	}
	if _, err := pdfHasSparseTextPage(ctx, []byte("garbage, not a pdf"), 1); err == nil {
		t.Error("corrupt input should return an extraction error")
	}
}

func TestIsPDFArtifact(t *testing.T) {
	cases := []struct {
		name, filename, mime string
		want                 bool
	}{
		{"pdf mime", "x", "application/pdf", true},
		{"pdf mime with params", "x", "application/pdf; charset=binary", true},
		{"octet-stream with .pdf ext", "scan.pdf", "application/octet-stream", true},
		{"empty mime with .pdf ext", "scan.PDF", "", true},
		{"non-pdf mime", "scan.pdf", "image/png", false},
		{"octet-stream no ext", "scan", "application/octet-stream", false},
		{"no mime no ext", "scan", "", false},
	}
	for _, tc := range cases {
		if got := isPDFArtifact(tc.filename, tc.mime); got != tc.want {
			t.Errorf("%s: isPDFArtifact(%q,%q) = %v, want %v", tc.name, tc.filename, tc.mime, got, tc.want)
		}
	}
}

func TestRunPDFParse(t *testing.T) {
	if v, err := runPDFParse(t.Context(), func() (int, error) { return 42, nil }); err != nil || v != 42 {
		t.Errorf("success path: got (%d, %v), want (42, nil)", v, err)
	}

	if _, err := runPDFParse(t.Context(), func() (int, error) { panic("boom") }); err == nil {
		t.Error("panic in parse fn should surface as an error, not crash")
	}

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runPDFParse(ctx, func() (int, error) { <-blocked; return 0, nil }); err == nil {
		t.Error("cancelled context should abort the parse with an error")
	}
}
