// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var notProviderConfigKeys = map[string]bool{cfgMaxResults: true}

func TestEveryConfigKeyIsClassified(t *testing.T) {
	t.Parallel()
	declared := declaredConfigKeys(t)
	if len(declared) == 0 {
		t.Fatal("found no cfg* constants; the test no longer reads the package")
	}
	for name, key := range declared {
		if IsProviderConfigKey(key) == notProviderConfigKeys[key] {
			t.Errorf("%s (%q): classify it as a provider key or list it in notProviderConfigKeys", name, key)
		}
	}
}

func declaredConfigKeys(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]string)
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if !strings.HasPrefix(name.Name, "cfg") || i >= len(spec.Values) {
					continue
				}
				if lit, ok := spec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					value, _ := strconv.Unquote(lit.Value)
					keys[name.Name] = value
				}
			}
			return true
		})
	}
	return keys
}
