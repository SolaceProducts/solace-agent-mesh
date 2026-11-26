// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"strings"
	"testing"
)

func TestConvertCSVToMarkdown(t *testing.T) {
	csv := []byte("name,age\nAlice,30\nBob,25")
	got, err := convertCSVToMarkdown(csv)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "| name | age |") {
		t.Errorf("missing header row: %q", got)
	}
	if !strings.Contains(got, "| --- | --- |") {
		t.Errorf("missing separator row: %q", got)
	}
	if !strings.Contains(got, "| Alice | 30 |") {
		t.Errorf("missing data row: %q", got)
	}
}

func TestConvertCSVToMarkdownEmpty(t *testing.T) {
	got, err := convertCSVToMarkdown([]byte(""))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestConvertHTMLToMarkdownBasic(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"<h1>Title</h1>", "# Title"},
		{"<h2>Sub</h2>", "## Sub"},
		{"<p>Text</p>", "Text"},
		{"<strong>bold</strong>", "**bold**"},
		{"<em>italic</em>", "*italic*"},
		{"<a href=\"http://x.com\">link</a>", "[link](http://x.com)"},
		{"<code>x</code>", "`x`"},
	}
	for _, c := range cases {
		got, err := convertHTMLToMarkdown([]byte(c.in))
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%q → %q, want contains %q", c.in, got, c.want)
		}
	}
}

func TestConvertHTMLToMarkdownList(t *testing.T) {
	got, err := convertHTMLToMarkdown([]byte("<ul><li>a</li><li>b</li></ul>"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "- a") || !strings.Contains(got, "- b") {
		t.Errorf("expected bullet list, got %q", got)
	}

	got, err = convertHTMLToMarkdown([]byte("<ol><li>a</li><li>b</li></ol>"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1. a") || !strings.Contains(got, "2. b") {
		t.Errorf("expected numbered list, got %q", got)
	}
}

func TestConvertHTMLToMarkdownStripsScripts(t *testing.T) {
	got, err := convertHTMLToMarkdown([]byte("<p>keep</p><script>alert(1)</script><style>x{}</style>"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "alert") || strings.Contains(got, "x{}") {
		t.Errorf("script/style should be stripped, got %q", got)
	}
	if !strings.Contains(got, "keep") {
		t.Errorf("paragraph text should survive, got %q", got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"hello world", 5, "hello"},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncateUTF8(c.in, c.max); got != c.want {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}

	s := "ab\xc3\xa9"
	got := truncateUTF8(s, 3)
	if got != "ab" {
		t.Errorf("truncateUTF8 should not split multi-byte rune, got %q", got)
	}
}

func TestDispatchPDFRoutesElsewhere(t *testing.T) {
	cases := []struct {
		mime string
		ext  string
	}{
		{"application/pdf", ""},
		{"", ".pdf"},
		{"application/pdf", ".pdf"},
	}
	for _, c := range cases {
		_, err := dispatchConversion([]byte("doesn't matter"), c.mime, c.ext)
		if _, ok := err.(pdfHandledElsewhereError); !ok {
			t.Errorf("mime=%q ext=%q: expected pdfHandledElsewhereError, got %v", c.mime, c.ext, err)
		}
	}
}
