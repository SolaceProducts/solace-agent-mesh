// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"bytes"
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func convertHTMLToMarkdown(content []byte) (string, error) {
	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}
	var buf strings.Builder
	htmlNodeToMarkdown(&buf, doc)
	return strings.TrimSpace(buf.String()), nil
}

func htmlNodeToMarkdown(buf *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		buf.WriteString(n.Data)
	case html.ElementNode:
		switch n.DataAtom {
		case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
			level := int(n.Data[1] - '0')
			buf.WriteString(strings.Repeat("#", level) + " ")
			htmlRenderChildren(buf, n)
			buf.WriteString("\n\n")
		case atom.P, atom.Div:
			htmlRenderChildren(buf, n)
			buf.WriteString("\n\n")
		case atom.Br:
			buf.WriteString("\n")
		case atom.Strong, atom.B:
			buf.WriteString("**")
			htmlRenderChildren(buf, n)
			buf.WriteString("**")
		case atom.Em, atom.I:
			buf.WriteString("*")
			htmlRenderChildren(buf, n)
			buf.WriteString("*")
		case atom.A:
			href := htmlGetAttr(n, "href")
			if href != "" {
				buf.WriteString("[")
				htmlRenderChildren(buf, n)
				buf.WriteString("](")
				buf.WriteString(href)
				buf.WriteString(")")
			} else {
				htmlRenderChildren(buf, n)
			}
		case atom.Code:
			buf.WriteString("`")
			htmlRenderChildren(buf, n)
			buf.WriteString("`")
		case atom.Pre:
			buf.WriteString("```\n")
			htmlRenderChildren(buf, n)
			buf.WriteString("\n```\n\n")
		case atom.Blockquote:
			buf.WriteString("> ")
			htmlRenderChildren(buf, n)
			buf.WriteString("\n\n")
		case atom.Li:
			if n.Parent != nil && n.Parent.DataAtom == atom.Ol {
				idx := 1
				for s := n.Parent.FirstChild; s != nil; s = s.NextSibling {
					if s == n {
						break
					}
					if s.Type == html.ElementNode && s.DataAtom == atom.Li {
						idx++
					}
				}
				fmt.Fprintf(buf, "%d. ", idx)
			} else {
				buf.WriteString("- ")
			}
			htmlRenderChildren(buf, n)
			buf.WriteString("\n")
		case atom.Ul, atom.Ol:
			htmlRenderChildren(buf, n)
			buf.WriteString("\n")
		case atom.Script, atom.Style:
			return
		default:
			htmlRenderChildren(buf, n)
		}
	default:
		htmlRenderChildren(buf, n)
	}
}

func htmlRenderChildren(buf *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		htmlNodeToMarkdown(buf, c)
	}
}

func htmlGetAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
