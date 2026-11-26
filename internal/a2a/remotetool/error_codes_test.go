// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

var upperSnakeCase = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)

func TestAllErrorCodesListsEveryConstant(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "protocol.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[ErrorCode]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "ErrorCode" {
				continue
			}
			for i, name := range vs.Names {
				value, err := strconv.Unquote(vs.Values[i].(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				declared[ErrorCode(value)] = name.Name
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no ErrorCode constants; the source scan is broken")
	}
	listed := AllErrorCodes()
	for code, name := range declared {
		if !slices.Contains(listed, code) {
			t.Errorf("%s (%s) is missing from AllErrorCodes", name, code)
		}
		if !upperSnakeCase.MatchString(string(code)) {
			t.Errorf("%s = %q is not UPPER_SNAKE_CASE", name, code)
		}
	}
	if len(listed) != len(declared) {
		t.Errorf("AllErrorCodes has %d entries for %d constants", len(listed), len(declared))
	}
}
