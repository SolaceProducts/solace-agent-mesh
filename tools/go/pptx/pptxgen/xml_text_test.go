// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const textGoldenDir = "testdata/text"

var textCases = []struct {
	name string
	text string
	opts TextOptions
}{
	{
		name: "minimal",
		text: "Hello",
		opts: TextOptions{X: 1, Y: 1, Width: 4, Height: 1},
	},
	{
		name: "title_bold_centered",
		text: "Quarterly Results",
		opts: TextOptions{
			X: 1, Y: 2, Width: 8, Height: 1.6,
			FontFace: "Calibri", FontSize: 44,
			FontColor: "1F2937",
			Bold:      true,
			Align:     AlignCenter, VAlign: VAlignMiddle,
		},
	},
	{
		name: "body_with_fill",
		text: "This is body text.",
		opts: TextOptions{
			X: 0.5, Y: 1.2, Width: 9, Height: 4,
			FontFace: "Arial", FontSize: 18,
			FontColor: "374151",
			Align:     AlignLeft, VAlign: VAlignTop,
			Fill: "F3F4F6",
		},
	},
	{
		name: "right_bottom",
		text: "Footer",
		opts: TextOptions{
			X: 0, Y: 6, Width: 10, Height: 1,
			FontSize: 10,
			Align:    AlignRight, VAlign: VAlignBottom,
		},
	},
	{
		name: "italic_quote",
		text: "Less is more.",
		opts: TextOptions{
			X: 1, Y: 2, Width: 8, Height: 4,
			FontFace: "Georgia", FontSize: 26, Italic: true,
			FontColor: "111827",
			VAlign:    VAlignMiddle,
		},
	},
	{
		name: "bold_italic_colored",
		text: "Critical",
		opts: TextOptions{
			X: 1, Y: 1, Width: 4, Height: 1,
			FontFace: "Calibri", FontSize: 24,
			FontColor: "DC2626",
			Bold:      true, Italic: true,
		},
	},
	{
		name: "line_spacing_1_5",
		text: "Wider",
		opts: TextOptions{
			X: 1, Y: 1, Width: 4, Height: 2,
			FontSize:    14,
			LineSpacing: 1.5,
		},
	},
	{
		name: "multi_line",
		text: "Line one\nLine two\nLine three",
		opts: TextOptions{
			X: 1, Y: 1, Width: 6, Height: 3,
			FontSize: 16,
		},
	},
	{
		name: "crlf_collapses",
		text: "A\r\nB",
		opts: TextOptions{
			X: 0, Y: 0, Width: 4, Height: 1,
			FontSize: 12,
		},
	},
	{
		name: "xml_escape",
		text: "1 < 2 & 3 > 0 \"q\" 'a'",
		opts: TextOptions{
			X: 0, Y: 0, Width: 4, Height: 1,
			FontSize: 12,
		},
	},
	{
		name: "empty_text",
		text: "",
		opts: TextOptions{
			X: 0, Y: 0, Width: 4, Height: 1,
			FontSize: 12,
		},
	},
}

func TestTextElementGoldens(t *testing.T) {
	for _, c := range textCases {
		t.Run(c.name, func(t *testing.T) {
			el := &textElement{text: c.text, opts: c.opts}
			var b strings.Builder
			el.emitXML(&b, newIDAllocator())
			got := []byte(b.String())

			path := filepath.Join(textGoldenDir, c.name+".xml")
			if *updateGolden {
				if err := os.MkdirAll(textGoldenDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run with -update to seed)", path, err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("text fragment %q differs from golden\n--- want ---\n%s\n--- got ---\n%s",
					c.name, want, got)
			}
		})
	}
}

func TestTextElementWellFormed(t *testing.T) {
	for _, c := range textCases {
		t.Run(c.name, func(t *testing.T) {
			el := &textElement{text: c.text, opts: c.opts}
			var b strings.Builder
			el.emitXML(&b, newIDAllocator())
			doc := wrapNS(b.String())
			if err := parseAll([]byte(doc)); err != nil {
				t.Fatalf("malformed XML for %s: %v\n%s", c.name, err, doc)
			}
		})
	}
}

func TestTextEndToEnd(t *testing.T) {
	p := New()
	p.setFixedTime(fixedTimeForGolden)
	s := p.AddSlide()
	s.AddText("Hello", TextOptions{X: 1, Y: 1, Width: 4, Height: 1, FontSize: 18})

	out, err := p.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes: %v", err)
	}
	slide := findEntry(t, out, "ppt/slides/slide1.xml")
	if !bytes.Contains(slide, []byte(`<p:sp><p:nvSpPr><p:cNvPr id="2" name="TextBox 2"/>`)) {
		t.Errorf("text frame not present in slide1.xml:\n%s", slide)
	}
	if !bytes.Contains(slide, []byte(`<a:t>Hello</a:t>`)) {
		t.Errorf("text content missing in slide1.xml:\n%s", slide)
	}
}

func wrapNS(frag string) string {
	const root = `<root xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">`
	return root + frag + `</root>`
}
