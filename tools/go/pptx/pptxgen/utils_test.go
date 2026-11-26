// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"strings"
	"testing"
)

func TestInchToEMU(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0, 0},
		{1, 914400},
		{0.5, 457200},
		{10, 9144000},
		{-1, -914400},
		{1.0 / emuPerInch, 1},
	}
	for _, c := range cases {
		if got := inchToEMU(c.in); got != c.want {
			t.Errorf("inchToEMU(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPointToEMU(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0, 0},
		{1, 12700},
		{72, 914400},
		{-2, -25400},
	}
	for _, c := range cases {
		if got := pointToEMU(c.in); got != c.want {
			t.Errorf("pointToEMU(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNormalizeColorHex_ReportsTheCharacterTheCallerWrote(t *testing.T) {
	_, err := NormalizeColorHex("café1")
	if err == nil {
		t.Fatal("expected an error for a non-hex character")
	}
	if !strings.Contains(err.Error(), "é") {
		t.Errorf("message should name the offending character 'é', got: %v", err)
	}
	if strings.Contains(err.Error(), "Ã") {
		t.Errorf("message names a character the caller never wrote: %v", err)
	}
}

func TestParseColor(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"#abcdef", "ABCDEF"},
		{"abcdef", "ABCDEF"},
		{"  #1A2B3C  ", "1A2B3C"},
		{"#abc", "AABBCC"},
		{"#xyzxyz", ""},
		{"123", "112233"},
		{"too-long-input", ""},
	}
	for _, c := range cases {
		if got := parseColor(c.in); got != c.want {
			t.Errorf("parseColor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEscapeXML(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"<tag>", "&lt;tag&gt;"},
		{"a & b", "a &amp; b"},
		{`he said "hi"`, "he said &quot;hi&quot;"},
		{"it's", "it&apos;s"},
		{"hello\x00world", "helloworld"},
		{"a\x07b\x1fc", "abc"},
		{"line\tone\nline\rtwo", "line\tone\nline\rtwo"},
	}
	for _, c := range cases {
		if got := escapeXML(c.in); got != c.want {
			t.Errorf("escapeXML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIDAllocator(t *testing.T) {
	a := newIDAllocator()
	if got := a.Next(); got != 2 {
		t.Errorf("first Next = %d, want 2", got)
	}
	if got := a.Next(); got != 3 {
		t.Errorf("second Next = %d, want 3", got)
	}
}

func TestRelIDAllocator(t *testing.T) {
	a := newRelIDAllocator(2)
	want := []string{"rId2", "rId3", "rId4"}
	for i, w := range want {
		if got := a.Next(); got != w {
			t.Errorf("Next #%d = %q, want %q", i, got, w)
		}
	}
}
