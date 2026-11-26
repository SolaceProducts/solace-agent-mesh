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
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var codeHelpers = map[string][]string{
	"requestFailureCode": {"pkg/tools/web_request.go"},
	"errorCode":          {"pkg/tools/image_tools.go", "pkg/tools/web_search_request.go"},
	"imageAPIErrorCode":  {"pkg/tools/image_tools.go"},
}

var codeBuilders = map[string]bool{"DispatchErrorCode": true, "HTTPFailureCode": true, "HILRefusalErrorCode": true}

var codePassthroughs = map[string]bool{"internal/tool/remote/tool.go": true}

func TestErrorCodesAreNamedConstants(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	calls := 0
	for _, dir := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if d.Name() == "testdata" || rel == "pkg/samtoolsdk" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || codePassthroughs[rel] {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			calls += checkErrorCodes(t, fset, file, rel)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls == 0 {
		t.Fatal("found no WithErrorCode calls; the scan is broken")
	}
}

func checkErrorCodes(t *testing.T, fset *token.FileSet, file *ast.File, rel string) int {
	t.Helper()
	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if calledName(node.Fun) != "WithErrorCode" || !isToolCall(node.Fun, file.Name.Name) {
				return true
			}
			calls++
			if len(node.Args) != 1 || !isNamedErrorCode(node.Args[0]) {
				t.Errorf("%s: WithErrorCode takes a literal or unnamed code; name it in internal/taxonomy",
					fset.Position(node.Pos()))
			}
		case *ast.FuncDecl:
			if !slices.Contains(codeHelpers[node.Name.Name], rel) || node.Body == nil {
				return true
			}
			ast.Inspect(node.Body, func(inner ast.Node) bool {
				if _, nested := inner.(*ast.FuncLit); nested {
					return false
				}
				if ret, ok := inner.(*ast.ReturnStmt); ok && (len(ret.Results) != 1 || !isNamedErrorCode(ret.Results[0])) {
					t.Errorf("%s: %s returns a literal or unnamed code; name it in internal/taxonomy",
						fset.Position(ret.Pos()), node.Name.Name)
				}
				return true
			})
		}
		return true
	})
	return calls
}

func isToolCall(fun ast.Expr, pkgName string) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return pkgName == "tool"
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		return ok && pkg.Name == "tool"
	}
	return false
}

func calledName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func isNamedErrorCode(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING && e.Value == `""`
	case *ast.Ident:
		return strings.HasPrefix(e.Name, "ErrorCode")
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			return false
		}
		switch pkg.Name {
		case "taxonomy":
			return strings.HasPrefix(e.Sel.Name, "ErrorCode") || strings.HasPrefix(e.Sel.Name, "ToolFailure")
		case "tool":
			return strings.HasPrefix(e.Sel.Name, "ErrorCode")
		}
	case *ast.CallExpr:
		name := calledName(e.Fun)
		_, helper := codeHelpers[name]
		return codeBuilders[name] || helper
	}
	return false
}
