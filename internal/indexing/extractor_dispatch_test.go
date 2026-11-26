// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

type fakePDFExtractor struct {
	called   bool
	filename string
}

func (f *fakePDFExtractor) Extract(_ context.Context, filename string, _ []byte) (*convert.Result, error) {
	f.called = true
	f.filename = filename
	return &convert.Result{Text: "stub", CitationType: "page"}, nil
}

func TestConvertOneRoutesPDFsThroughExtractor(t *testing.T) {
	fake := &fakePDFExtractor{}
	res, err := convertOne(t.Context(), fake, "report.pdf", []byte("fake-pdf"), "")
	if err != nil {
		t.Fatalf("convertOne err: %v", err)
	}
	if !fake.called {
		t.Fatal("expected extractor to be called for .pdf filename")
	}
	if fake.filename != "report.pdf" {
		t.Errorf("filename forwarded incorrectly: %q", fake.filename)
	}
	if res.Text != "stub" {
		t.Errorf("expected stub text from extractor, got %q", res.Text)
	}
}

func TestConvertOneRoutesPDFsByMIME(t *testing.T) {
	fake := &fakePDFExtractor{}
	_, err := convertOne(t.Context(), fake, "noext", []byte("x"), "application/pdf")
	if err != nil {
		t.Fatalf("convertOne err: %v", err)
	}
	if !fake.called {
		t.Fatal("expected extractor to be called when MIME is application/pdf")
	}
}

func TestConvertOneSkipsExtractorForNonPDF(t *testing.T) {
	fake := &fakePDFExtractor{}
	_, err := convertOne(t.Context(), fake, "notes.txt", []byte("hello"), "text/plain")
	if err != nil {
		t.Fatalf("convertOne err: %v", err)
	}
	if fake.called {
		t.Error("extractor must not be invoked for non-PDF files")
	}
}

func TestConvertOneNilExtractorFallsBackToConvert(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("convertOne panicked with nil extractor: %v", r)
		}
	}()
	_, _ = convertOne(t.Context(), nil, "report.pdf", []byte("not-a-pdf"), "application/pdf")
}

func TestIsPDFCases(t *testing.T) {
	cases := []struct {
		filename string
		mime     string
		want     bool
	}{
		{"a.pdf", "", true},
		{"a.PDF", "", true},
		{"noext", "application/pdf", true},
		{"a.docx", "", false},
		{"a.txt", "text/plain", false},
		{"", "", false},
	}
	for _, c := range cases {
		got := isPDF(c.filename, c.mime)
		if got != c.want {
			t.Errorf("isPDF(%q, %q) = %v, want %v", c.filename, c.mime, got, c.want)
		}
	}
}

var _ context.Context
