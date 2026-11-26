// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import "testing"

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
		got := ConvertMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("ConvertMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvertMarkdownKeepingLinks(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"a link is left as written", "See [the report](https://example.com/r) now.", "See [the report](https://example.com/r) now."},
		{"a URL with parentheses is not cut short", "See [Wiki](https://en.wikipedia.org/wiki/Go_(language)) now.", "See [Wiki](https://en.wikipedia.org/wiki/Go_(language)) now."},
		{"bold is still converted next to a link", "**Done**: [r](https://example.com/r)", "*Done*: [r](https://example.com/r)"},
		{"a heading is still converted", "# Title\nbody", "\n*Title*\nbody"},
		{"a code fence keeps its link and loses its language tag", "```go\n[a](https://x.io)\n```", "```\n[a](https://x.io)\n```"},
		{"empty text stays empty", "", ""},
	} {
		if got := ConvertMarkdownKeepingLinks(tt.input); got != tt.want {
			t.Errorf("%s: ConvertMarkdownKeepingLinks(%q) = %q, want %q", tt.name, tt.input, got, tt.want)
		}
	}
}

func TestConvertMarkdown_Bold(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"**bold text**", "*bold text*"},
		{"some **bold** words", "some *bold* words"},
	}
	for _, tt := range tests {
		got := ConvertMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("ConvertMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
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
		got := ConvertMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("ConvertMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConvertMarkdown_CodeBlocksPreserved(t *testing.T) {
	input := "before\n```python\n**not bold** [not a link](url)\n```\nafter **bold**"
	got := ConvertMarkdown(input)

	if !contains(got, "**not bold**") {
		t.Errorf("code block content should not be converted: %q", got)
	}
	if !contains(got, "*bold*") {
		t.Errorf("text outside code blocks should be converted: %q", got)
	}
}

func TestConvertMarkdown_CodeBlockLanguageStripped(t *testing.T) {
	input := "```python\nprint('hello')\n```"
	got := ConvertMarkdown(input)
	if contains(got, "python") {
		t.Errorf("language tag should be stripped: %q", got)
	}
	if !contains(got, "```\nprint('hello')\n```") {
		t.Errorf("code content should be preserved: %q", got)
	}
}

func TestConvertMarkdown_HorizontalRule(t *testing.T) {
	for _, rule := range []string{"---", "***", "___", "  ----  "} {
		in := "above\n" + rule + "\nbelow"
		got := ConvertMarkdown(in)
		if !contains(got, slackDivider) {
			t.Errorf("ConvertMarkdown(%q) = %q, want a divider line", in, got)
		}
		if contains(got, "---") || contains(got, "***") || contains(got, "___") {
			t.Errorf("ConvertMarkdown(%q) = %q, rule chars should be replaced", in, got)
		}
	}
}

func TestConvertMarkdown_TableSeparatorNotTreatedAsRule(t *testing.T) {
	got := ConvertMarkdown("| A | B |\n|---|---|\n| 1 | 2 |")
	if contains(got, slackDivider) {
		t.Errorf("table separator was converted to a divider: %q", got)
	}
}

func TestConvertMarkdown_Empty(t *testing.T) {
	if got := ConvertMarkdown(""); got != "" {
		t.Errorf("ConvertMarkdown(\"\") = %q, want \"\"", got)
	}
}

func TestConvertMarkdown_NoConversion(t *testing.T) {
	plain := "just some plain text"
	if got := ConvertMarkdown(plain); got != plain {
		t.Errorf("ConvertMarkdown(%q) = %q, want unchanged", plain, got)
	}
}
