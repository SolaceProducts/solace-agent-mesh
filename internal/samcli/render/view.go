// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package render

import (
	"slices"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	gmtext "github.com/yuin/goldmark/text"
)

type Segment struct {
	Kind   string  `json:"kind"`
	Name   string  `json:"name,omitempty"`
	Status string  `json:"status,omitempty"`
	Text   string  `json:"-"`
	Blocks []Block `json:"blocks,omitempty"`
}

type Block struct {
	Kind    string     `json:"kind"`
	Preview string     `json:"preview,omitempty"`
	Lang    string     `json:"lang,omitempty"`
	Header  []string   `json:"header,omitempty"`
	Rows    int        `json:"rows,omitempty"`
	Cells   [][]string `json:"-"`
	Text    string     `json:"-"`
}

const (
	blockHeading   = "heading"
	blockParagraph = "paragraph"
	blockTable     = "table"
	blockCode      = "code"
	blockList      = "list"
	blockQuote     = "quote"
	blockHTML      = "html"
	blockRule      = "rule"

	previewLen = 80
)

var gfm = goldmark.New(goldmark.WithExtensions(extension.GFM))

func Segments(b Bubble) []Segment {
	var segs []Segment
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			src := text.String()
			segs = append(segs, Segment{Kind: partText, Text: src, Blocks: Blocks(src)})
			text.Reset()
		}
	}
	for _, p := range b.Parts {
		switch p.Kind {
		case partText:
			text.WriteString(p.Text)
		case partFile, partArtifact:
			if p.Hidden || fileShownAsCard(p, b.Parts) {
				continue
			}
			flush()
			segs = append(segs, Segment{Kind: p.Kind, Name: p.Name, Status: p.Status})
		}
	}
	flush()
	return segs
}

func fileShownAsCard(p Part, parts []Part) bool {
	if p.Kind != partFile || p.Name == "" {
		return false
	}
	return slices.ContainsFunc(parts, func(o Part) bool {
		return o.Kind == partArtifact && o.Name == p.Name && o.Status != statusFailed
	})
}

func Blocks(src string) []Block {
	source := []byte(strings.ReplaceAll(strings.ReplaceAll(src, "&laquo;", "«"), "&raquo;", "»"))
	doc := gfm.Parser().Parse(gmtext.NewReader(source))
	var blocks []Block
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		b := Block{Text: nodeText(n, source)}
		switch node := n.(type) {
		case *ast.Heading:
			b.Kind = blockHeading
		case *ast.Paragraph, *ast.TextBlock:
			b.Kind = blockParagraph
		case *ast.FencedCodeBlock:
			b.Kind = blockCode
			b.Lang = string(node.Language(source))
		case *ast.CodeBlock:
			b.Kind = blockCode
		case *ast.List:
			b.Kind = blockList
		case *ast.Blockquote:
			b.Kind = blockQuote
		case *ast.HTMLBlock:
			b.Kind = blockHTML
		case *ast.ThematicBreak:
			b.Kind = blockRule
		case *east.Table:
			b.Kind = blockTable
			b.Header, b.Cells = tableCells(node, source)
			b.Rows = len(b.Cells)
		default:
			b.Kind = n.Kind().String()
		}
		b.Preview = preview(b.Text)
		blocks = append(blocks, b)
	}
	return blocks
}

func tableCells(t *east.Table, source []byte) ([]string, [][]string) {
	var header []string
	var rows [][]string
	for r := t.FirstChild(); r != nil; r = r.NextSibling() {
		var cells []string
		for c := r.FirstChild(); c != nil; c = c.NextSibling() {
			cells = append(cells, strings.TrimSpace(nodeText(c, source)))
		}
		if _, ok := r.(*east.TableHeader); ok {
			header = cells
			continue
		}
		rows = append(rows, cells)
	}
	return header, rows
}

func nodeText(n ast.Node, source []byte) string {
	if lines := n.Lines(); lines != nil && lines.Len() > 0 {
		var sb strings.Builder
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			sb.Write(seg.Value(source))
		}
		return sb.String()
	}
	var sb strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(source))
			if t.SoftLineBreak() || t.HardLineBreak() {
				sb.WriteByte('\n')
			}
		case *ast.String:
			sb.Write(t.Value)
		default:
			if c != n && c.Type() == ast.TypeBlock && c.Lines() != nil && c.Lines().Len() > 0 {
				sb.WriteString(nodeText(c, source))
				sb.WriteByte('\n')
				return ast.WalkSkipChildren, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > previewLen {
		return string(r[:previewLen]) + "…"
	}
	return s
}
