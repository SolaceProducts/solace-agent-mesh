// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	gwAuthPkg   = "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	authtestPkg = gwAuthPkg + "/authtest"
)

var constructorFile = regexp.MustCompile(`^caller_.*\.go$|^auth\.go$|^runas\.go$`)

type callerConstruction struct{ site, what string }

func TestOnlyTheConstructorsBuildACaller(t *testing.T) {
	allowed := map[string]string{
		"internal/entrypoint/auth/subject_testsupport.go#NewTestSubject": "test-only fixture builder",
	}

	var found []callerConstruction
	for _, root := range []string{"internal", "cmd", "pkg"} {
		err := filepath.Walk(filepath.Join("..", "..", "..", root), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			rel := repoRelative(p)
			if strings.HasPrefix(rel, "internal/entrypoint/auth/authtest/") {
				return nil
			}
			src, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			sites, scanErr := scanCallerConstructions(rel, src)
			if scanErr != nil {
				t.Errorf("%s: %v", rel, scanErr)
				return nil
			}
			found = append(found, sites...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	matched := map[string]bool{}
	var offenders []string
	for _, c := range found {
		if _, ok := allowed[c.site]; ok {
			matched[c.site] = true
			continue
		}
		offenders = append(offenders, c.site+": "+c.what)
	}
	if len(offenders) > 0 {
		t.Errorf("production code builds a gateway caller directly:\n  %s\n\n"+
			"Build it through a constructor in internal/entrypoint/auth instead. If the site genuinely "+
			"cannot, add file#function to this test's allowlist WITH the reason.",
			strings.Join(offenders, "\n  "))
	}
	for site := range allowed {
		if !matched[site] {
			t.Errorf("allowlist entry %q matches no construction any more — remove it", site)
		}
	}
}

func TestScanCallerConstructions(t *testing.T) {
	cases := map[string]struct {
		rel  string
		src  string
		want []string
	}{
		"plain import, pointer literal": {
			rel:  "internal/entrypoint/x/a.go",
			src:  `package x; import "` + gwAuthPkg + `"; func F() any { return &auth.Subject{UserID: "u"} }`,
			want: []string{"internal/entrypoint/x/a.go#F"},
		},
		"aliased entrypoint/auth next to an aliased internal/auth": {
			rel: "internal/entrypoint/x/b.go",
			src: `package x; import (coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"; "` + gwAuthPkg + `")
				func G() (any, any) { return coreauth.Identity{}, auth.Subject{} }`,
			want: []string{"internal/entrypoint/x/b.go#G"},
		},
		"new()": {
			rel:  "internal/entrypoint/x/c.go",
			src:  `package x; import gw "` + gwAuthPkg + `"; func H() any { return new(gw.Subject) }`,
			want: []string{"internal/entrypoint/x/c.go#H"},
		},
		"unqualified inside entrypoint/auth, outside a constructor file": {
			rel:  "internal/entrypoint/auth/remote.go",
			src:  `package auth; type Subject struct{}; func R() *Subject { return &Subject{} }`,
			want: []string{"internal/entrypoint/auth/remote.go#R"},
		},
		"unqualified inside a constructor file is allowed": {
			rel: "internal/entrypoint/auth/caller_x.go",
			src: `package auth; type Subject struct{}; func ForX() *Subject { return &Subject{} }`,
		},
		"only the agent-side type": {
			rel: "internal/entrypoint/x/d.go",
			src: `package x; import auth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"; func D() any { return auth.Identity{} }`,
		},
		"production calling the test seam": {
			rel:  "internal/entrypoint/x/f.go",
			src:  `package x; import gw "` + gwAuthPkg + `"; func S() any { return gw.NewTestSubject(gw.SubjectFields{}) }`,
			want: []string{"internal/entrypoint/x/f.go#S"},
		},
		"production building SubjectFields": {
			rel:  "internal/entrypoint/x/g.go",
			src:  `package x; import "` + gwAuthPkg + `"; func T() any { return auth.SubjectFields{UserID: "u"} }`,
			want: []string{"internal/entrypoint/x/g.go#T"},
		},
		"the seam called inside entrypoint/auth, outside a constructor file": {
			rel:  "internal/entrypoint/auth/noop.go",
			src:  `package auth; func N() any { return NewTestSubject(SubjectFields{}) }`,
			want: []string{"internal/entrypoint/auth/noop.go#N"},
		},
		"the seam called inside a constructor file": {
			rel:  "internal/entrypoint/auth/caller_x.go",
			src:  `package auth; func ForY() any { return NewTestSubject(SubjectFields{}) }`,
			want: []string{"internal/entrypoint/auth/caller_x.go#ForY"},
		},
		"the file declaring the seam": {
			rel: "internal/entrypoint/auth/subject_testsupport.go",
			src: `package auth; type SubjectFields struct{}; func NewTestSubject(f SubjectFields) any { return f }`,
		},
		"production blank-importing authtest": {
			rel:  "internal/entrypoint/x/h.go",
			src:  `package x; import _ "` + gwAuthPkg + `/authtest"`,
			want: []string{"internal/entrypoint/x/h.go#import"},
		},
		"package-level var": {
			rel:  "internal/entrypoint/x/i.go",
			src:  `package x; import "` + gwAuthPkg + `"; var fallback = &auth.Subject{}`,
			want: []string{"internal/entrypoint/x/i.go#fallback"},
		},
		"package-level var calling the test seam": {
			rel:  "internal/entrypoint/x/j.go",
			src:  `package x; import gw "` + gwAuthPkg + `"; var seeded = gw.NewTestSubject(gw.SubjectFields{})`,
			want: []string{"internal/entrypoint/x/j.go#seeded"},
		},
		"the test seam taken as a value": {
			rel:  "internal/entrypoint/x/k.go",
			src:  `package x; import "` + gwAuthPkg + `"; func K() { newSubject := auth.NewTestSubject; _ = newSubject }`,
			want: []string{"internal/entrypoint/x/k.go#K"},
		},
		"the test seam taken as a value inside entrypoint/auth": {
			rel:  "internal/entrypoint/auth/l.go",
			src:  `package auth; func L() { f := NewTestSubject; _ = f }`,
			want: []string{"internal/entrypoint/auth/l.go#L"},
		},
		"a comment and a type mention are not constructions": {
			rel: "internal/entrypoint/x/e.go",
			src: `package x; import "` + gwAuthPkg + `"
				// auth.Subject{UserID: "u"} in a comment
				func E(id *auth.Subject) *auth.Subject { return id }`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := scanCallerConstructions(tc.rel, []byte(tc.src))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			var sites []string
			for _, c := range got {
				sites = append(sites, c.site)
			}
			if !slices.Equal(sites, tc.want) {
				t.Errorf("sites = %v, want %v", sites, tc.want)
			}
		})
	}
}

func scanCallerConstructions(rel string, src []byte) ([]callerConstruction, error) {
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, 0)
	if err != nil {
		return nil, err
	}
	insideAuth := path.Dir(rel) == "internal/entrypoint/auth"
	inConstructorFile := insideAuth && constructorFile.MatchString(path.Base(rel))
	declaresSeam := insideAuth && path.Base(rel) == "subject_testsupport.go"

	var out []callerConstruction
	aliases := map[string]string{}
	for _, im := range f.Imports {
		ip, uerr := strconv.Unquote(im.Path.Value)
		if uerr != nil {
			continue
		}
		name := path.Base(ip)
		if im.Name != nil {
			name = im.Name.Name
		}
		aliases[name] = ip
		if ip == authtestPkg && !insideAuth {
			out = append(out, callerConstruction{rel + "#import", "imports authtest (test-support only)"})
		}
	}
	isCallerType := func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			return ok && x.Sel.Name == "Subject" && aliases[id.Name] == gwAuthPkg
		case *ast.Ident:
			return insideAuth && !inConstructorFile && x.Name == "Subject"
		}
		return false
	}
	isSeam := func(e ast.Expr, name string) bool {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			return ok && !insideAuth && x.Sel.Name == name && aliases[id.Name] == gwAuthPkg
		case *ast.Ident:
			return insideAuth && !declaresSeam && x.Name == name
		}
		return false
	}

	scan := func(node ast.Node, site string) {
		reported := false
		ast.Inspect(node, func(n ast.Node) bool {
			if reported {
				return false
			}
			switch x := n.(type) {
			case *ast.CompositeLit:
				switch {
				case isCallerType(x.Type):
					out = append(out, callerConstruction{site, "composite literal"})
					reported = true
				case isSeam(x.Type, "SubjectFields"):
					out = append(out, callerConstruction{site, "builds auth.SubjectFields (the test seam)"})
					reported = true
				}
			case *ast.CallExpr:
				switch {
				case isIdent(x.Fun, "new") && len(x.Args) == 1 && isCallerType(x.Args[0]):
					out = append(out, callerConstruction{site, "new()"})
					reported = true
				case isSeam(x.Fun, "NewTestSubject"):
					out = append(out, callerConstruction{site, "calls auth.NewTestSubject (the test seam)"})
					reported = true
				}
			case *ast.SelectorExpr, *ast.Ident:
				if isSeam(x.(ast.Expr), "NewTestSubject") {
					out = append(out, callerConstruction{site, "references auth.NewTestSubject (the test seam)"})
					reported = true
				}
			}
			return true
		})
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			scan(d, rel+"#"+d.Name.Name)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					scan(vs, rel+"#"+vs.Names[0].Name)
				}
			}
		}
	}
	return out, nil
}

func repoRelative(p string) string {
	rel := filepath.ToSlash(p)
	for _, root := range []string{"internal/", "cmd/", "pkg/"} {
		if i := strings.Index(rel, root); i >= 0 {
			return rel[i:]
		}
	}
	return rel
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
