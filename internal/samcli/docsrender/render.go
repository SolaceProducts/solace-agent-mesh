// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	gmtext "github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/frontmatter"
)

const urlPrefix = "/docs/documentation"

type Renderer struct {
	fsys fs.FS
	md   goldmark.Markdown
}

type frontMatter struct {
	Title           string `yaml:"title"`
	Description     string `yaml:"description"`
	SidebarPosition *int   `yaml:"sidebar_position"`
}

func New(fsys fs.FS) *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			&frontmatter.Extender{},
			newAdmonitionExtension(),
			newMermaidExtension(),
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(chromahtml.WithClasses(false)),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	return &Renderer{fsys: fsys, md: md}
}

type Page struct {
	Title       string
	Description string
	BodyHTML    string
	TOC         []TOCEntry
}

type TOCEntry struct {
	Level int
	Text  string
	ID    string
}

func (r *Renderer) RenderFile(docPath string) (*Page, error) {
	src, err := fs.ReadFile(r.fsys, docPath)
	if err != nil {
		return nil, fmt.Errorf("read doc %q: %w", docPath, err)
	}

	ctx := parser.NewContext()
	doc := r.md.Parser().Parse(gmtext.NewReader(src), parser.WithContext(ctx))

	rewriteLinks(doc, dirOf(docPath))

	var buf bytes.Buffer
	if err := r.md.Renderer().Render(&buf, src, doc); err != nil {
		return nil, fmt.Errorf("render doc %q: %w", docPath, err)
	}

	page := &Page{BodyHTML: buf.String(), TOC: extractHeadings(doc, src)}
	if fm := frontmatter.Get(ctx); fm != nil {
		var meta frontMatter
		if err := fm.Decode(&meta); err == nil {
			page.Title = meta.Title
			page.Description = meta.Description
		}
	}
	if page.Title == "" {
		page.Title = titleFromPath(docPath)
	}
	return page, nil
}

func extractHeadings(doc ast.Node, src []byte) []TOCEntry {
	var toc []TOCEntry
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok || h.Level < 2 || h.Level > 3 {
			return ast.WalkContinue, nil
		}
		id, ok := h.AttributeString("id")
		if !ok {
			return ast.WalkSkipChildren, nil
		}
		idStr, ok := id.([]byte)
		if !ok {
			return ast.WalkSkipChildren, nil
		}
		text := headingText(h, src)
		if text != "" {
			toc = append(toc, TOCEntry{Level: h.Level, Text: text, ID: string(idStr)})
		}
		return ast.WalkSkipChildren, nil
	})
	return toc
}

func headingText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func titleFromPath(docPath string) string {
	base := docPath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".md")
	return strings.ReplaceAll(base, "-", " ")
}
