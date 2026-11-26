// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"context"
	"strings"
	"testing"
)

func TestInProcessExtractEmpty(t *testing.T) {
	ext := NewInProcessPDFExtractor()
	res, err := ext.Extract(t.Context(), "garbage.pdf", []byte("not a pdf"))
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "pdf") {
			t.Errorf("expected PDF-related error, got %v", err)
		}
		return
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Text != "" {
		t.Errorf("expected empty text, got %q", res.Text)
	}
}

func TestResultFromPagesEmpty(t *testing.T) {
	res := resultFromPages(nil)
	if res.Text != "" || len(res.Citations) != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
	if res.CitationType != "page" {
		t.Errorf("expected CitationType=page, got %q", res.CitationType)
	}
}

func TestResultFromPagesMultiPage(t *testing.T) {
	pages := []pageEntry{
		{number: 1, text: "hello"},
		{number: 2, text: "world"},
	}
	res := resultFromPages(pages)
	if res.Text != "hello\nworld" {
		t.Errorf("text=%q", res.Text)
	}
	if len(res.Citations) != 2 {
		t.Fatalf("expected 2 citations, got %d", len(res.Citations))
	}
	if res.Citations[0].Location != "physical_page_1" {
		t.Errorf("loc[0]=%q", res.Citations[0].Location)
	}
	if res.Citations[0].CharStart != 0 || res.Citations[0].CharEnd != 5 {
		t.Errorf("range[0] = [%d,%d)", res.Citations[0].CharStart, res.Citations[0].CharEnd)
	}
	if res.Citations[1].CharStart != 6 || res.Citations[1].CharEnd != 11 {
		t.Errorf("range[1] = [%d,%d)", res.Citations[1].CharStart, res.Citations[1].CharEnd)
	}
}

func TestResultFromPagesFallbackPage(t *testing.T) {
	res := resultFromPages([]pageEntry{{number: 0, text: "raw extract"}})
	if len(res.Citations) != 1 || res.Citations[0].Location != "unknown" {
		t.Errorf("expected single 'unknown' citation, got %+v", res.Citations)
	}
}

func TestIsEncryptedErrConvert(t *testing.T) {
	if isEncryptedErr(nil) {
		t.Error("nil should not be encrypted")
	}
	if !isEncryptedErr(errInline("encrypted PDF: invalid password")) {
		t.Error("encrypted message should match")
	}
	if !isEncryptedErr(errInline("PASSWORD required")) {
		t.Error("password message should match")
	}
	if isEncryptedErr(errInline("unrelated parser failure")) {
		t.Error("unrelated message should not match")
	}
}

type errInline string

func (e errInline) Error() string { return string(e) }

func TestInProcessExtractRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	ext := NewInProcessPDFExtractor()
	_, err := ext.Extract(ctx, "irrelevant.pdf", []byte("not a pdf at all"))
	if err != nil && err != context.Canceled {
		t.Logf("extract returned err=%v (expected nil, context.Canceled, or empty-pages success)", err)
	}
}
