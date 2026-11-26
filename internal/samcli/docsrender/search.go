// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
)

type searchDoc struct {
	Title string `json:"title"`
	Route string `json:"route"`
	Text  string `json:"text"`
}

func (r *Renderer) BuildSearchIndex() ([]searchDoc, error) {
	var docs []searchDoc
	err := fs.WalkDir(r.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rec, err := r.indexDoc(p)
		if err != nil {
			return err
		}
		docs = append(docs, rec)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("build search index: %w", err)
	}
	return docs, nil
}

func (r *Renderer) indexDoc(docPath string) (searchDoc, error) {
	src, err := fs.ReadFile(r.fsys, docPath)
	if err != nil {
		return searchDoc{}, fmt.Errorf("read doc %q: %w", docPath, err)
	}
	ctx := parser.NewContext()
	doc := r.md.Parser().Parse(gmtext.NewReader(src), parser.WithContext(ctx))

	title := ""
	if fm := frontmatter.Get(ctx); fm != nil {
		var meta frontMatter
		if fm.Decode(&meta) == nil {
			title = meta.Title
		}
	}
	if title == "" {
		title = titleFromPath(docPath)
	}
	return searchDoc{
		Title: title,
		Route: routeForDoc(docPath),
		Text:  extractText(doc, src),
	}, nil
}

func extractText(doc ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch t := n.(type) {
		case *ast.Text:
			if entering {
				b.Write(t.Segment.Value(src))
				if t.SoftLineBreak() || t.HardLineBreak() {
					b.WriteByte('\n')
				}
			}
		case *ast.FencedCodeBlock:
			if entering {
				appendLines(&b, t.Lines(), src)
			}
		case *ast.CodeBlock:
			if entering {
				appendLines(&b, t.Lines(), src)
			}
		default:
			if !entering && t.Type() == ast.TypeBlock {
				b.WriteByte('\n')
			}
		}
		return ast.WalkContinue, nil
	})
	return strings.ToLower(strings.Join(strings.Fields(b.String()), " "))
}

func appendLines(b *strings.Builder, lines *gmtext.Segments, src []byte) {
	for i := range lines.Len() {
		seg := lines.At(i)
		b.Write(seg.Value(src))
		b.WriteByte(' ')
	}
}
