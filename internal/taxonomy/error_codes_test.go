// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var upperSnakeCase = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*_?$`)

func errorCodeConstants(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, ident := range vs.Names {
					if !isErrorCodeName(ident.Name) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok {
						t.Fatalf("%s is not a string literal; every error code names its wire string", ident.Name)
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					found[ident.Name] = value
				}
			}
		}
	}
	return found
}

func isErrorCodeName(name string) bool {
	return strings.HasPrefix(name, "ErrorCode") || strings.HasPrefix(name, "ToolFailure") ||
		strings.HasSuffix(name, "ErrorCodePrefix")
}

func TestErrorCodeConstantsAreUpperSnakeCase(t *testing.T) {
	constants := errorCodeConstants(t)
	if len(constants) == 0 {
		t.Fatal("found no error code constants; the source scan is broken")
	}
	for name, value := range constants {
		if !upperSnakeCase.MatchString(value) {
			t.Errorf("%s = %q is not UPPER_SNAKE_CASE", name, value)
		}
	}
}

func TestCodeConstantsAreAllListed(t *testing.T) {
	for _, list := range []struct {
		prefix, name string
		codes        []string
	}{{"ToolFailure", "ToolFailureCodes", ToolFailureCodes}, {"ErrorCode", "RuntimeErrorCodes", RuntimeErrorCodes}} {
		var listed int
		for name, value := range errorCodeConstants(t) {
			if !strings.HasPrefix(name, list.prefix) {
				continue
			}
			listed++
			if !slices.Contains(list.codes, value) {
				t.Errorf("%s = %q is missing from %s", name, value, list.name)
			}
		}
		if listed != len(list.codes) {
			t.Errorf("%s has %d entries for %d constants", list.name, len(list.codes), listed)
		}
	}
}
