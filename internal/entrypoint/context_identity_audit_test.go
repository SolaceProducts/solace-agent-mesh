// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallerIdentityNeverTravelsThroughTheContext(t *testing.T) {
	rules := []struct {
		dir       string
		forbidden string
	}{
		{".", "SubjectFromContext"},
		{".", "ContextWithSubject"},
		{"slack", "ContextWithSubject"},
		{"teams", "ContextWithSubject"},
		{"googlechat", "ContextWithSubject"},
		{"whatsapp", "ContextWithSubject"},
		{"webhook", "ContextWithSubject"},
		{"eventmesh", "ContextWithSubject"},
		{"email", "ContextWithSubject"},
	}
	for _, rule := range rules {
		for _, site := range callsIn(t, rule.dir, rule.forbidden) {
			t.Errorf("%s calls auth.%s; pass the caller as an argument instead", site, rule.forbidden)
		}
	}
}

func callsIn(t *testing.T, dir, fn string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var sites []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == fn {
				sites = append(sites, fset.Position(call.Pos()).String())
			}
			return true
		})
	}
	return sites
}
