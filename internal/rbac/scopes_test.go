// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestScopeCatalog(t *testing.T) {
	consts := parseScopeCatalog(t, scopeSourceFiles()...)
	if len(consts) == 0 {
		t.Fatal("parsed 0 scope constants — parser likely broke")
	}

	seen := make(map[string]string)
	for _, c := range consts {
		t.Run(c.name, func(t *testing.T) {
			if !Pattern.MatchString(c.value) {
				t.Errorf("scope %q (%s) does not match rbac.Pattern %s", c.value, c.name, Pattern.String())
			}

			if prior, ok := seen[c.value]; ok {
				t.Errorf("scope %q (%s) duplicates the value of %s", c.value, c.name, prior)
			} else {
				seen[c.value] = c.name
			}

			segments := strings.Split(c.value, ":")
			if len(segments) != 3 {
				t.Errorf("scope %q (%s) must have exactly 3 segments, got %d", c.value, c.name, len(segments))
				return
			}

			for i, seg := range segments {
				if seg == "" {
					t.Errorf("scope %q (%s) has an empty segment at position %d", c.value, c.name, i+1)
				}
			}

			if bannedVerbs[segments[2]] {
				t.Errorf("scope %q (%s) uses banned verb %q in segment 3 — prefer read, create, update, delete or invoke", c.value, c.name, segments[2])
			}
		})
	}
}

func TestPlatformIdentityScopes(t *testing.T) {
	if len(PlatformIdentityScopes) == 0 {
		t.Fatal("PlatformIdentityScopes is empty — list likely broke")
	}
	seen := make(map[string]bool)
	for _, s := range PlatformIdentityScopes {
		if !Pattern.MatchString(s) {
			t.Errorf("scope %q does not match rbac.Pattern %s", s, Pattern.String())
		}
		if seen[s] {
			t.Errorf("scope %q duplicated in PlatformIdentityScopes", s)
		}
		seen[s] = true

		segments := strings.Split(s, ":")
		if len(segments) != 3 {
			t.Errorf("scope %q must have exactly 3 segments, got %d", s, len(segments))
			continue
		}
		if segments[0] != "tool" {
			t.Errorf("scope %q expected category 'tool', got %q", s, segments[0])
		}
		if bannedVerbs[segments[2]] {
			t.Errorf("scope %q uses banned verb %q in segment 3", s, segments[2])
		}
	}
}

var bannedVerbs = map[string]bool{
	"view":     true,
	"edit":     true,
	"write":    true,
	"manage":   true,
	"execute":  true,
	"delegate": true,
}

type scopeCatalogEntry struct {
	name  string
	value string
}

func parseScopeCatalog(t *testing.T, filenames ...string) []scopeCatalogEntry {
	t.Helper()
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(filenames))
	for _, filename := range filenames {
		file, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", filename, err)
		}
		files = append(files, file)
	}

	var out []scopeCatalogEntry
	visit := func(name string, valueExpr ast.Expr) {
		if !strings.HasPrefix(name, "Scope") {
			return
		}
		if valueExpr == nil {
			t.Errorf("identifier %s has no value expression — catalog tripwire: every Scope* must be a string literal", name)
			return
		}
		lit, ok := valueExpr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("identifier %s value is not a string literal (got %T) — catalog tripwire: every Scope* must be a string literal so the grammar checks see it", name, valueExpr)
			return
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Errorf("unquote %s = %s: %v", name, lit.Value, err)
			return
		}
		out = append(out, scopeCatalogEntry{name: name, value: val})
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			if gen.Tok != token.CONST && gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range vs.Names {
					var valueExpr ast.Expr
					if i < len(vs.Values) {
						valueExpr = vs.Values[i]
					}
					visit(ident.Name, valueExpr)
				}
			}
		}
	}
	return out
}
