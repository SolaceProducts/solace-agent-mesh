// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"strings"
	"testing"
)

func TestConvertMarkdown_Empty(t *testing.T) {
	if got := convertMarkdown(""); got != "" {
		t.Errorf("convertMarkdown(\"\") = %q, want \"\"", got)
	}
}

func TestConvertMarkdown_NoMarkdown(t *testing.T) {
	plain := "just some plain text"
	if got := convertMarkdown(plain); got != plain {
		t.Errorf("convertMarkdown(%q) = %q, want unchanged", plain, got)
	}
}

func TestConvertMarkdown_Bold(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"**bold text**", "*bold text*"},
		{"some **bold** words", "some *bold* words"},
		{"**a** and **b**", "*a* and *b*"},
	}
	for _, tt := range tests {
		got := convertMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("convertMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvertMarkdown_Links(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[Click here](https://example.com)", "<https://example.com|Click here>"},
		{"Visit [Google](https://google.com) now", "Visit <https://google.com|Google> now"},
		{"[a](http://x.com) and [b](http://y.com)", "<http://x.com|a> and <http://y.com|b>"},
	}
	for _, tt := range tests {
		got := convertMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("convertMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvertMarkdown_Headings(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"# Heading 1", "\n*Heading 1*"},
		{"## Heading 2", "\n*Heading 2*"},
		{"### Heading 3", "\n*Heading 3*"},
	}
	for _, tt := range tests {
		got := convertMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("convertMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvertMarkdown_CodeBlockPreserved(t *testing.T) {
	input := "before\n```\n**not bold** [not a link](url)\n```\nafter **bold**"
	got := convertMarkdown(input)

	if !strings.Contains(got, "**not bold**") {
		t.Errorf("code block content should not be converted: %q", got)
	}
	if !strings.Contains(got, "*bold*") {
		t.Errorf("text outside code blocks should be converted: %q", got)
	}
}

func TestConvertMarkdown_CodeBlockLanguageStripped(t *testing.T) {
	input := "```python\nprint('hello')\n```"
	got := convertMarkdown(input)

	if strings.Contains(got, "python") {
		t.Errorf("language tag should be stripped: %q", got)
	}
	if !strings.Contains(got, "```\nprint('hello')\n```") {
		t.Errorf("code content should be preserved: %q", got)
	}
}

func TestConvertMarkdown_Mixed(t *testing.T) {
	input := "# Summary\n**bold** text with [link](https://example.com)\n```go\nfmt.Println(\"hello\")\n```\n## Details"
	got := convertMarkdown(input)

	if !strings.Contains(got, "\n*Summary*") {
		t.Errorf("heading not converted: %q", got)
	}
	if !strings.Contains(got, "*bold*") {
		t.Errorf("bold not converted: %q", got)
	}
	if !strings.Contains(got, "<https://example.com|link>") {
		t.Errorf("link not converted: %q", got)
	}
	if strings.Contains(got, "```go") {
		t.Errorf("language tag should be stripped: %q", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("code content should be preserved: %q", got)
	}
	if !strings.Contains(got, "\n*Details*") {
		t.Errorf("second heading not converted: %q", got)
	}
}
