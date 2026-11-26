// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	stdhtml "html"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type mermaidNode struct {
	ast.BaseBlock
	Diagram string
}

var kindMermaid = ast.NewNodeKind("Mermaid")

func (n *mermaidNode) Kind() ast.NodeKind { return kindMermaid }

func (n *mermaidNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

func (n *mermaidNode) IsRaw() bool { return true }

type mermaidExtension struct{}

func newMermaidExtension() goldmark.Extender { return mermaidExtension{} }

func (mermaidExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithASTTransformers(
		util.Prioritized(mermaidTransformer{}, 0),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(mermaidRenderer{}, 100),
	))
}

type mermaidTransformer struct{}

func (mermaidTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	var targets []*ast.FencedCodeBlock
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if fcb, ok := n.(*ast.FencedCodeBlock); ok && string(fcb.Language(src)) == "mermaid" {
				targets = append(targets, fcb)
			}
		}
		return ast.WalkContinue, nil
	})

	for _, fcb := range targets {
		var diagram []byte
		lines := fcb.Lines()
		for i := range lines.Len() {
			seg := lines.At(i)
			diagram = append(diagram, seg.Value(src)...)
		}
		node := &mermaidNode{Diagram: string(diagram)}
		if parent := fcb.Parent(); parent != nil {
			parent.ReplaceChild(parent, fcb, node)
		}
	}
}

type mermaidRenderer struct{}

func (mermaidRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMermaid, renderMermaid)
}

func renderMermaid(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*mermaidNode)
		_, _ = w.WriteString(`<div class="mermaid">` + stdhtml.EscapeString(n.Diagram) + "</div>\n")
	}
	return ast.WalkSkipChildren, nil
}
