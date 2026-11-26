// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func productionFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found: %v", err)
	}
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, parseErr := parser.ParseFile(fset, file, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		parsed[file] = f
	}
	return fset, parsed
}

func TestNoDelete_OnlyTheRemovalRouteCallsTheRemovalCode(t *testing.T) {
	t.Parallel()
	removalCode := []string{"RemoveDropped", "removeOne", "deleteAfterUndeploy", "refuseWhileRunning", "createdBySession"}
	fset, parsed := productionFiles(t)
	for file, f := range parsed {
		if file == removalFile {
			continue
		}
		for _, decl := range f.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			insideRemovalHandler := isFunc && file == "routes.go" && fn.Name.Name == "handleRemove"
			ast.Inspect(decl, func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok || !slices.Contains(removalCode, ident.Name) {
					return true
				}
				if insideRemovalHandler && ident.Name == "RemoveDropped" {
					return true
				}
				t.Errorf("%s names %s: only %s, and handleRemove for RemoveDropped, may", fset.Position(ident.Pos()), ident.Name, removalFile)
				return true
			})
		}
	}
}

func TestNoDelete_NothingHereOpensARouteToAPIKeys(t *testing.T) {
	t.Parallel()
	fset, parsed := productionFiles(t)
	for _, f := range parsed {
		ast.Inspect(f, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok && ident.Name == "AllowAPIKeys" {
				t.Errorf("%s calls AllowAPIKeys: the build and removal routes are for a signed-in user only", fset.Position(ident.Pos()))
			}
			return true
		})
	}
}
