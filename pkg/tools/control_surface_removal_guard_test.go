// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var platformControlMethods = map[string]bool{"get": true, "post": true}

func TestBuilderTools_ControlCallsToThePlatformOnlyReadOrDiscover(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sources found: %v", err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, file, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", file, parseErr)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isCustomControlCall(call) {
				return true
			}
			calls++
			where := fset.Position(call.Pos()).String()
			app := stringLiteral(call.Args[1])
			if app == "" {
				if ident, isIdent := call.Args[1].(*ast.Ident); !isIdent || ident.Name != "gatewayControlApp" {
					t.Errorf("%s: control call to an app that is not a literal or gatewayControlApp", where)
				}
				return true
			}
			method := stringLiteral(call.Args[2])
			if app != "platform" {
				t.Errorf("%s: a tool sends to the %q control app; tools may only read from the platform", where, app)
			}
			if !platformControlMethods[method] {
				t.Errorf("%s: a tool sends %q to the platform; only get and post (read-only checks and discovery) are allowed", where, method)
			}
			return true
		})
	}
	if calls == 0 {
		t.Fatal("found no control calls: the scan is not looking at the tools")
	}
}

func isCustomControlCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Custom" && len(call.Args) >= 4
}

func stringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
