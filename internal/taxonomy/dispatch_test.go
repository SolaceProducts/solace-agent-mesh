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
	"slices"
	"strconv"
	"strings"
	"testing"
)

func dispatchClassConstants(t *testing.T) map[string]string {
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
				collectDispatchClass(t, spec.(*ast.ValueSpec), found)
			}
		}
	}
	return found
}

func collectDispatchClass(t *testing.T, vs *ast.ValueSpec, found map[string]string) {
	t.Helper()
	for i, name := range vs.Names {
		if !strings.HasPrefix(name.Name, "DispatchClass") {
			continue
		}
		if i >= len(vs.Values) {
			t.Fatalf("%s has no explicit value; every dispatch class must name its wire string", name.Name)
		}
		lit, ok := vs.Values[i].(*ast.BasicLit)
		if !ok {
			t.Fatalf("%s is not a string literal", name.Name)
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		found[name.Name] = value
	}
}

func TestDispatchClassConstantsAreAllListed(t *testing.T) {
	constants := dispatchClassConstants(t)
	if len(constants) == 0 {
		t.Fatal("found no DispatchClass constants; the source scan is broken")
	}
	for name, value := range constants {
		if !slices.Contains(DispatchErrorClasses, value) {
			t.Errorf("%s = %q is missing from DispatchErrorClasses", name, value)
		}
	}
	if len(DispatchErrorClasses) != len(constants) {
		t.Errorf("DispatchErrorClasses has %d entries for %d constants; every class must be a named constant",
			len(DispatchErrorClasses), len(constants))
	}
	for _, class := range DispatchErrorClasses {
		if class != strings.ToLower(class) || strings.ContainsAny(class, " -") {
			t.Errorf("class %q is not a lower snake_case token", class)
		}
	}
}
