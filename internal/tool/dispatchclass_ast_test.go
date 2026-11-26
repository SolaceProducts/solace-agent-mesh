// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestClassifierReturnsOnlyNamedDispatchClasses(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dispatcherror.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var classifier *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "ClassifyDispatchError" {
			classifier = fn
		}
	}
	if classifier == nil {
		t.Fatal("ClassifyDispatchError not found in dispatcherror.go")
	}
	returns := 0
	ast.Inspect(classifier.Body, func(n ast.Node) bool {
		if _, nested := n.(*ast.FuncLit); nested {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		returns++
		if len(ret.Results) != 1 || !isNamedDispatchClassOrEmpty(ret.Results[0]) {
			t.Errorf("%s: ClassifyDispatchError returns something other than \"\" or a taxonomy.DispatchClass constant",
				fset.Position(ret.Pos()))
		}
		return true
	})
	if returns == 0 {
		t.Fatal("found no return statements; the scan is broken")
	}
}

func isNamedDispatchClassOrEmpty(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING && e.Value == `""`
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		return ok && pkg.Name == "taxonomy" && strings.HasPrefix(e.Sel.Name, "DispatchClass")
	}
	return false
}
