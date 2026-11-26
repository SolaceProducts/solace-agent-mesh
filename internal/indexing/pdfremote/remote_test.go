// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfremote

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func TestMapPagesToResultEmpty(t *testing.T) {
	res := mapPagesToResult(nil)
	if res.Text != "" {
		t.Errorf("expected empty text, got %q", res.Text)
	}
	if res.CitationType != "page" {
		t.Errorf("expected page citation type, got %q", res.CitationType)
	}
	if len(res.Citations) != 0 {
		t.Errorf("expected no citations, got %d", len(res.Citations))
	}
}

func TestMapPagesToResultMultiPage(t *testing.T) {
	pages := []any{
		map[string]any{"number": float64(1), "text": "abc"},
		map[string]any{"number": float64(2), "text": "de"},
		map[string]any{"number": float64(3), "text": "fghij"},
	}
	res := mapPagesToResult(pages)
	want := "abc\nde\nfghij"
	if res.Text != want {
		t.Errorf("text=%q, want %q", res.Text, want)
	}
	if got := len(res.Citations); got != 3 {
		t.Fatalf("expected 3 citations, got %d", got)
	}
	expect := []convert.CitationRange{
		{Location: "physical_page_1", CharStart: 0, CharEnd: 3},
		{Location: "physical_page_2", CharStart: 4, CharEnd: 6},
		{Location: "physical_page_3", CharStart: 7, CharEnd: 12},
	}
	for i, c := range expect {
		got := res.Citations[i]
		if got.Location != c.Location || got.CharStart != c.CharStart || got.CharEnd != c.CharEnd {
			t.Errorf("citation[%d] = %+v, want %+v", i, got, c)
		}
		if res.Text[got.CharStart:got.CharEnd] == "" {
			t.Errorf("citation[%d] maps to empty slice", i)
		}
	}
}

func TestMapPagesToResultFallbackPage(t *testing.T) {
	pages := []any{
		map[string]any{"number": float64(0), "text": "recovered text"},
	}
	res := mapPagesToResult(pages)
	if len(res.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(res.Citations))
	}
	if res.Citations[0].Location != "unknown" {
		t.Errorf("expected Location=unknown for fallback page, got %q", res.Citations[0].Location)
	}
}

func TestMapPagesToResultSkipsEmptyAndMalformed(t *testing.T) {
	pages := []any{
		map[string]any{"number": float64(1), "text": "first"},
		"not-an-object",
		map[string]any{"number": float64(2), "text": ""},
		map[string]any{"number": float64(3), "text": "third"},
	}
	res := mapPagesToResult(pages)
	if got := len(res.Citations); got != 2 {
		t.Fatalf("expected 2 citations, got %d", got)
	}
	if res.Text != "first\nthird" {
		t.Errorf("unexpected text %q", res.Text)
	}
	if res.Citations[0].Location != "physical_page_1" {
		t.Errorf("first citation wrong location: %q", res.Citations[0].Location)
	}
	if res.Citations[1].Location != "physical_page_3" {
		t.Errorf("second citation wrong location: %q", res.Citations[1].Location)
	}
}

func TestExtractRequiresExecutor(t *testing.T) {
	e := New(Config{})
	if e == nil {
		t.Fatal("New returned nil")
	}
	_, err := e.Extract(t.Context(), "any.pdf", nil)
	if err == nil {
		t.Fatal("expected error when executor not configured")
	}
}

func TestNewDefaultsAreApplied(t *testing.T) {
	e := New(Config{})
	if e.timeoutSeconds != 120 {
		t.Errorf("expected default timeout 120s, got %d", e.timeoutSeconds)
	}
	if e.corrIDPrefix != "pdf-extract" {
		t.Errorf("expected default corr-id prefix, got %q", e.corrIDPrefix)
	}
	if e.logger == nil {
		t.Error("expected non-nil logger from default")
	}
	if e.authToken != "" {
		t.Errorf("expected empty default auth token, got %q", e.authToken)
	}
}

func TestNewPropagatesAuthToken(t *testing.T) {
	const token = "test.jwt.token"
	e := New(Config{AuthToken: token})
	if e.authToken != token {
		t.Errorf("expected auth token to be propagated, got %q", e.authToken)
	}
}

func TestExtractPagesFieldHandlesShapes(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want int
	}{
		{
			name: "nested under data",
			in:   map[string]any{"data": map[string]any{"pages": []any{1, 2, 3}}},
			want: 3,
		},
		{
			name: "top-level fallback",
			in:   map[string]any{"pages": []any{1, 2}},
			want: 2,
		},
		{
			name: "neither — error result with status field",
			in:   map[string]any{"status": "error", "message": "boom"},
			want: 0,
		},
		{
			name: "data wrong type",
			in:   map[string]any{"data": "not-a-map"},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractPagesField(c.in)
			if len(got) != c.want {
				t.Errorf("expected %d pages, got %d", c.want, len(got))
			}
		})
	}
}

func TestInterpretToolResultStatusError(t *testing.T) {
	resp := &remote.InvokeResponse{
		Result: remotetool.InvokeResult{
			ToolResult: map[string]any{
				"status":  "error",
				"message": "PDF extraction failed: PDF is encrypted",
			},
		},
	}
	_, err := interpretToolResult(resp)
	if err == nil {
		t.Fatal("expected error for status=error result")
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("expected error to carry tool message, got %q", err.Error())
	}
}

func TestInterpretToolResultStatusErrorNoMessage(t *testing.T) {
	resp := &remote.InvokeResponse{
		Result: remotetool.InvokeResult{
			ToolResult: map[string]any{"status": "error"},
		},
	}
	_, err := interpretToolResult(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "tool returned error") {
		t.Errorf("expected default error text, got %q", err.Error())
	}
}

func TestInterpretToolResultSuccess(t *testing.T) {
	resp := &remote.InvokeResponse{
		Result: remotetool.InvokeResult{
			ToolResult: map[string]any{
				"status": "success",
				"data": map[string]any{
					"pages": []any{
						map[string]any{"number": float64(1), "text": "hello"},
					},
				},
			},
		},
	}
	res, err := interpretToolResult(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "hello" {
		t.Errorf("text=%q", res.Text)
	}
}

func TestInterpretToolResultEmpty(t *testing.T) {
	res, err := interpretToolResult(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "" {
		t.Errorf("expected empty text, got %q", res.Text)
	}

	res, err = interpretToolResult(&remote.InvokeResponse{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "" {
		t.Errorf("expected empty text, got %q", res.Text)
	}
}

func TestCorrIDsAreUniqueAcrossExtractors(t *testing.T) {
	a := New(Config{CorrIDPrefix: "pdf-extract-ns1"})
	b := New(Config{CorrIDPrefix: "pdf-extract-ns1"})

	const n = 50
	seen := make(map[string]struct{}, 2*n)
	for range n {
		idA := a.nextCorrID()
		idB := b.nextCorrID()
		if _, dup := seen[idA]; dup {
			t.Fatalf("duplicate corrID %q from extractor A", idA)
		}
		if _, dup := seen[idB]; dup {
			t.Fatalf("duplicate corrID %q from extractor B", idB)
		}
		seen[idA] = struct{}{}
		seen[idB] = struct{}{}
		if idA == idB {
			t.Fatalf("corrIDs collided across extractors: %q", idA)
		}
	}
}
