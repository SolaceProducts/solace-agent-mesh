// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	stdhtml "html"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var admonitionKinds = map[string]bool{
	"note": true, "tip": true, "info": true,
	"warning": true, "danger": true, "caution": true,
}

type admonitionNode struct {
	ast.BaseBlock
	Variant string
	Title   string
}

var kindAdmonition = ast.NewNodeKind("Admonition")

func (n *admonitionNode) Kind() ast.NodeKind { return kindAdmonition }

func (n *admonitionNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"Variant": n.Variant, "Title": n.Title}, nil)
}

type admonitionParser struct{}

func (admonitionParser) Trigger() []byte { return []byte{':'} }

func (admonitionParser) Open(parent ast.Node, reader text.Reader, _ parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	s := strings.TrimRight(string(line), "\r\n")
	rest, ok := strings.CutPrefix(s, ":::")
	if !ok {
		return nil, parser.NoChildren
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || !admonitionKinds[fields[0]] {
		return nil, parser.NoChildren
	}
	node := &admonitionNode{Variant: fields[0]}
	if len(fields) > 1 {
		node.Title = strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
	}
	reader.Advance(len(line))
	return node, parser.HasChildren
}

func (admonitionParser) Continue(_ ast.Node, reader text.Reader, _ parser.Context) parser.State {
	line, _ := reader.PeekLine()
	if strings.TrimRight(string(line), "\r\n") == ":::" {
		reader.Advance(len(line))
		return parser.Close
	}
	return parser.Continue | parser.HasChildren
}

func (admonitionParser) Close(_ ast.Node, _ text.Reader, _ parser.Context) {
}

func (admonitionParser) CanInterruptParagraph() bool { return true }

func (admonitionParser) CanAcceptIndentedLine() bool { return false }

type admonitionRenderer struct{}

func (admonitionRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindAdmonition, renderAdmonition)
}

func renderAdmonition(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*admonitionNode)
	if entering {
		_, _ = w.WriteString(`<div class="admonition admonition-` + n.Variant + `">`)
		title := n.Title
		if title == "" {
			title = strings.ToUpper(n.Variant[:1]) + n.Variant[1:]
		}
		_, _ = w.WriteString(`<div class="admonition-title">` + stdhtml.EscapeString(title) + `</div>`)
	} else {
		_, _ = w.WriteString(`</div>`)
	}
	return ast.WalkContinue, nil
}

type admonitionExtension struct{}

func newAdmonitionExtension() goldmark.Extender { return admonitionExtension{} }

func (admonitionExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		util.Prioritized(admonitionParser{}, 100),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(admonitionRenderer{}, 100),
	))
}
