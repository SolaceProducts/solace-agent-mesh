// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var allowedClearCredentialForwarders = map[string]string{
	filepath.Join("openapi", "oauth.go"): "ClearCredential",
}

func TestCompliance_NoDirectClearCredentialInToolPackages(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var violations []string
	exemptionsUsed := map[string]bool{}
	walkErr := filepath.WalkDir(pkgDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(pkgDir, path)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			exempt := isFunc && fn.Recv != nil && allowedClearCredentialForwarders[rel] == fn.Name.Name
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "ClearCredential" {
					if exempt {
						exemptionsUsed[rel] = true
						return true
					}
					line := fset.Position(sel.Sel.Pos()).Line
					violations = append(violations, rel+":"+strconv.Itoa(line))
				}
				return true
			})
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	for file, method := range allowedClearCredentialForwarders {
		if !exemptionsUsed[file] {
			t.Errorf("allowedClearCredentialForwarders[%q] = %q no longer forwards ClearCredential — drop the entry", file, method)
		}
	}
	if len(violations) > 0 {
		t.Errorf("Found %d direct ClearCredential reference(s) under internal/tool.\n"+
			"Use toolauth.AuthHandler.ClearCredentialForReauth instead, which preserves "+
			"seeded credentials for eval-triggered tasks.\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
