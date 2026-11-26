// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"
)

func docsSourceFS(t *testing.T) fs.FS {
	t.Helper()
	const rel = "../../../docs/docs/documentation"
	if _, err := os.Stat(rel); err != nil {
		t.Skipf("docs source not found at %s: %v", rel, err)
	}
	return os.DirFS(rel)
}

func TestRelativeLinksResolve(t *testing.T) {
	fsys := docsSourceFS(t)
	srv, err := NewServer(fsys)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	r := srv.renderer

	var pages int
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		pages++
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		doc := r.md.Parser().Parse(gmtext.NewReader(src), parser.WithContext(parser.NewContext()))
		baseDir := dirOf(p)
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			link, ok := n.(*ast.Link)
			if !ok {
				return ast.WalkContinue, nil
			}
			route, changed := ResolveLink(baseDir, string(link.Destination))
			if !changed {
				return ast.WalkContinue, nil
			}
			rel := strings.TrimPrefix(route, urlPrefix+"/")
			if i := strings.IndexByte(rel, '#'); i >= 0 {
				rel = rel[:i]
			}
			if rel == "" {
				rel = strings.TrimPrefix(srv.IntroRoute(), urlPrefix+"/")
			}
			if _, ok := srv.resolveDoc(rel); !ok {
				t.Errorf("%s: link %q resolves to route %q which has no page",
					p, string(link.Destination), route)
			}
			return ast.WalkContinue, nil
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if pages == 0 {
		t.Fatal("no markdown pages found")
	}
	t.Logf("checked relative links across %d pages", pages)
}

func TestIntroRenders(t *testing.T) {
	fsys := docsSourceFS(t)
	srv, err := NewServer(fsys)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rel := strings.TrimPrefix(srv.IntroRoute(), urlPrefix+"/")
	docPath, ok := srv.resolveDoc(rel)
	if !ok {
		t.Fatalf("landing route %q resolves to no doc", srv.IntroRoute())
	}
	page, err := srv.renderer.RenderFile(docPath)
	if err != nil {
		t.Fatalf("RenderFile(%q): %v", docPath, err)
	}
	if page.Title == "" || !strings.Contains(page.BodyHTML, "<") {
		t.Fatalf("landing page did not render: title=%q bodyLen=%d", page.Title, len(page.BodyHTML))
	}
}
