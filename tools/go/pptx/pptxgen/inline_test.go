// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInlineMarkdown(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []inlineRun
	}{
		{
			name: "plain text is one run",
			in:   "just words",
			want: []inlineRun{{text: "just words"}},
		},
		{
			name: "bold splits into three runs",
			in:   "a **b** c",
			want: []inlineRun{{text: "a "}, {text: "b", bold: true}, {text: " c"}},
		},
		{
			name: "leading bold label",
			in:   "**Event-Driven AI:** Advocates running loops",
			want: []inlineRun{{text: "Event-Driven AI:", bold: true}, {text: " Advocates running loops"}},
		},
		{
			name: "underscore bold",
			in:   "__strong__ tail",
			want: []inlineRun{{text: "strong", bold: true}, {text: " tail"}},
		},
		{
			name: "italic asterisk",
			in:   "an *emphasised* word",
			want: []inlineRun{{text: "an "}, {text: "emphasised", italic: true}, {text: " word"}},
		},
		{
			name: "italic underscore",
			in:   "an _emphasised_ word",
			want: []inlineRun{{text: "an "}, {text: "emphasised", italic: true}, {text: " word"}},
		},
		{
			name: "code span monospace and literal",
			in:   "call `IsEnabled(key)` now",
			want: []inlineRun{{text: "call "}, {text: "IsEnabled(key)", code: true}, {text: " now"}},
		},
		{
			name: "link keeps text drops url",
			in:   "see [the docs](https://x.test/y) here",
			want: []inlineRun{{text: "see "}, {text: "the docs"}, {text: " here"}},
		},
		{
			name: "bold containing italic nests",
			in:   "**bold _and italic_**",
			want: []inlineRun{{text: "bold ", bold: true}, {text: "and italic", bold: true, italic: true}},
		},
		{
			name: "intraword underscore is literal",
			in:   "the user_id field",
			want: []inlineRun{{text: "the user_id field"}},
		},
		{
			name: "spaced asterisks are literal multiplication",
			in:   "compute 3 * 4 * 5",
			want: []inlineRun{{text: "compute 3 * 4 * 5"}},
		},
		{
			name: "unmatched marker is literal",
			in:   "a lone ** here",
			want: []inlineRun{{text: "a lone ** here"}},
		},
		{
			name: "empty input yields no runs",
			in:   "",
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseInlineMarkdown(c.in, true)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseInlineMarkdown(%q)\n got = %#v\nwant = %#v", c.in, got, c.want)
			}
		})
	}
}

func TestMarkdownEmitProducesStyledRuns(t *testing.T) {
	el := &textElement{
		text: "**Bold** and *it* and `code`",
		opts: TextOptions{X: 1, Y: 1, Width: 8, Height: 1, FontSize: 18, FontFace: "Arial", Markdown: true},
	}
	var b strings.Builder
	el.emitXML(&b, newIDAllocator())
	got := b.String()

	if n := strings.Count(got, "<a:r>"); n < 5 {
		t.Errorf("expected multiple runs, got %d:\n%s", n, got)
	}
	if !strings.Contains(got, `<a:rPr lang="en-US" sz="1800" b="1">`) {
		t.Errorf("bold run rPr missing:\n%s", got)
	}
	if !strings.Contains(got, `<a:rPr lang="en-US" sz="1800" i="1">`) {
		t.Errorf("italic run rPr missing:\n%s", got)
	}
	if !strings.Contains(got, `<a:t>Bold</a:t>`) || !strings.Contains(got, `<a:t>code</a:t>`) {
		t.Errorf("run text missing:\n%s", got)
	}
	if !strings.Contains(got, `<a:latin typeface="`+monospaceFont+`"/>`) {
		t.Errorf("code run should use monospace face:\n%s", got)
	}
	for _, marker := range []string{"<a:t>**", "<a:t>`", "*</a:t>"} {
		if strings.Contains(got, marker) {
			t.Errorf("literal marker %q leaked into output:\n%s", marker, got)
		}
	}

	if err := parseAll([]byte(wrapNS(got))); err != nil {
		t.Fatalf("malformed XML: %v\n%s", err, got)
	}
}

func TestMarkdownHighlightRun(t *testing.T) {
	el := &textElement{
		text: "Not a chatbot. ==A shared platform== everyone builds on.",
		opts: TextOptions{
			X: 1, Y: 1, Width: 8, Height: 1, FontSize: 32, FontFace: "Arial",
			FontColor: "093B5F", HighlightColor: "ABFF88", Markdown: true,
		},
	}
	var b strings.Builder
	el.emitXML(&b, newIDAllocator())
	got := b.String()

	if !strings.Contains(got, `<a:srgbClr val="ABFF88"/></a:solidFill>`) {
		t.Errorf("highlight run should use HighlightColor ABFF88:\n%s", got)
	}
	if !strings.Contains(got, `<a:srgbClr val="093B5F"/></a:solidFill>`) {
		t.Errorf("non-highlight runs should keep FontColor 093B5F:\n%s", got)
	}
	if !strings.Contains(got, `<a:t>A shared platform</a:t>`) {
		t.Errorf("highlight text missing:\n%s", got)
	}
	if strings.Contains(got, "==") {
		t.Errorf("highlight markers leaked into output:\n%s", got)
	}
	if err := parseAll([]byte(wrapNS(got))); err != nil {
		t.Fatalf("malformed XML: %v\n%s", err, got)
	}
}

func TestMarkdownHighlightLiteralWithoutColor(t *testing.T) {
	el := &textElement{
		text: "latency == p99 == fast",
		opts: TextOptions{X: 0, Y: 0, Width: 4, Height: 1, FontSize: 12, FontColor: "000000", Markdown: true},
	}
	var b strings.Builder
	el.emitXML(&b, newIDAllocator())
	got := b.String()
	if !strings.Contains(got, `<a:t>latency == p99 == fast</a:t>`) {
		t.Errorf("without HighlightColor, == must stay literal in one run:\n%s", got)
	}
}

func TestMarkdownEmitEmptyParagraph(t *testing.T) {
	el := &textElement{text: "", opts: TextOptions{X: 0, Y: 0, Width: 4, Height: 1, FontSize: 12, Markdown: true}}
	var b strings.Builder
	el.emitXML(&b, newIDAllocator())
	got := b.String()
	if strings.Count(got, "<a:r>") != 1 || !strings.Contains(got, "<a:t></a:t>") {
		t.Errorf("empty markdown paragraph should have one empty run:\n%s", got)
	}
	if err := parseAll([]byte(wrapNS(got))); err != nil {
		t.Fatalf("malformed XML: %v\n%s", err, got)
	}
}
