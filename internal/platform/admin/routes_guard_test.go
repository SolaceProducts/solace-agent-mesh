// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var scopeExemptRoutes = map[string]string{}

const minExpectedRegistrations = 6

func TestPlatformRoutesAreScopeGuarded(t *testing.T) {
	regs := collectRouteRegistrations(t)
	if len(regs) < minExpectedRegistrations {
		t.Fatalf("found %d route registrations, expected at least %d — the AST walker likely no longer matches the registration pattern", len(regs), minExpectedRegistrations)
	}

	matchedExemptions := make(map[string]bool)
	for _, reg := range regs {
		if reg.guarded() {
			continue
		}
		if !reg.patternResolved {
			t.Errorf("%s: route handler is not wrapped in a guard (requireScope/requireAnyOfScope/requireSuperAdmin/guardSuperAdmin) and its route pattern cannot be resolved statically — guard it, or register it with a resolvable pattern so scopeExemptRoutes can name it", reg.position)
			continue
		}
		if _, ok := scopeExemptRoutes[reg.pattern]; ok {
			matchedExemptions[reg.pattern] = true
			continue
		}
		t.Errorf("%s: route %q is not scope-guarded — wrap the handler in requireScope/requireAnyOfScope/requireSuperAdmin/guardSuperAdmin, or add it to scopeExemptRoutes with a reason and get it reviewed", reg.position, reg.pattern)
	}

	for pattern := range scopeExemptRoutes {
		if !matchedExemptions[pattern] {
			t.Errorf("scopeExemptRoutes entry %q matched no unguarded registration — remove the stale entry (the route was guarded, renamed, or deleted)", pattern)
		}
	}
}

type routeRegistration struct {
	position        string
	registrar       string
	pattern         string
	patternResolved bool
	guardName       string
}

func (r routeRegistration) guarded() bool { return r.guardName != "" }

var routeSourceDirs = []string{".", "../builderapply"}

func collectRouteRegistrations(t *testing.T) []routeRegistration {
	t.Helper()

	var regs []routeRegistration
	for _, dir := range routeSourceDirs {
		regs = append(regs, collectDirRegistrations(t, dir)...)
	}
	return regs
}

func collectDirRegistrations(t *testing.T, dir string) []routeRegistration {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package dir %s: %v", dir, err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}

	pkgConsts := make(map[string]string)
	for _, file := range files {
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok {
				collectStringConsts(gen, pkgConsts)
			}
		}
	}

	var regs []routeRegistration
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			regs = append(regs, collectFromFunc(fset, fn, pkgConsts)...)
		}
	}
	return regs
}

func collectFromFunc(fset *token.FileSet, fn *ast.FuncDecl, pkgConsts map[string]string) []routeRegistration {
	consts := make(map[string]string, len(pkgConsts))
	for k, v := range pkgConsts {
		consts[k] = v
	}
	guardedIdents := make(map[string]string)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.DeclStmt:
			if gen, ok := node.Decl.(*ast.GenDecl); ok {
				collectStringConsts(gen, consts)
			}
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || i >= len(node.Rhs) {
					continue
				}
				guard := guardCallName(node.Rhs[i])
				if prev, seen := guardedIdents[ident.Name]; seen {
					guard = weakerGuard(prev, guard)
				}
				guardedIdents[ident.Name] = guard
			}
		}
		return true
	})

	var regs []routeRegistration
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		var pattern string
		var resolved bool
		var handler ast.Expr
		switch {
		case (sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle") && len(call.Args) == 2:
			pattern, resolved = resolveStringExpr(call.Args[0], consts)
			handler = call.Args[1]
		case sel.Sel.Name == "HandleWebUIRoute" && len(call.Args) == 4:
			pattern, resolved = webUIRoutePattern(call.Args[1], call.Args[2], consts)
			handler = call.Args[3]
		default:
			return true
		}
		regs = append(regs, routeRegistration{
			position:        fset.Position(call.Pos()).String(),
			registrar:       fn.Name.Name,
			pattern:         pattern,
			patternResolved: resolved,
			guardName:       guardNameForHandler(handler, guardedIdents),
		})
		return true
	})
	return regs
}

const v1PlatformPrefix = "/api/v1/platform"

var httpMethodConsts = map[string]string{
	"MethodGet":    "GET",
	"MethodPost":   "POST",
	"MethodPut":    "PUT",
	"MethodPatch":  "PATCH",
	"MethodDelete": "DELETE",
}

func webUIRoutePattern(method, suffix ast.Expr, consts map[string]string) (string, bool) {
	var verb string
	if sel, ok := method.(*ast.SelectorExpr); ok {
		verb = httpMethodConsts[sel.Sel.Name]
	} else if v, ok := resolveStringExpr(method, consts); ok {
		verb = v
	}
	tail, ok := resolveStringExpr(suffix, consts)
	if verb == "" || !ok {
		return "", false
	}
	return verb + " " + v1PlatformPrefix + tail, true
}

func collectStringConsts(gen *ast.GenDecl, into map[string]string) {
	if gen.Tok != token.CONST {
		return
	}
	for _, spec := range gen.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, ident := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if val, ok := resolveStringExpr(vs.Values[i], into); ok {
				into[ident.Name] = val
			}
		}
	}
}

func guardNameForHandler(expr ast.Expr, guardedIdents map[string]string) string {
	if name := guardCallName(expr); name != "" {
		return name
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return guardedIdents[ident.Name]
	}
	return ""
}

func weakerGuard(a, b string) string {
	if a == "" || b == "" {
		return ""
	}
	if a == b {
		return a
	}
	if !superAdminGuards[a] {
		return a
	}
	return b
}

var guardFuncs = map[string]bool{
	"requireScope":               true,
	"requireAnyOfScope":          true,
	"RequireAnyOfScope":          true,
	"requireSuperAdmin":          true,
	"guardSuperAdmin":            true,
	"guardInteractiveSuperAdmin": true,
	"requireSuperAdminToRead":    true,
	"requireScopeToRead":         true,
}

var superAdminGuards = map[string]bool{
	"requireSuperAdmin":          true,
	"guardSuperAdmin":            true,
	"guardInteractiveSuperAdmin": true,
	"requireSuperAdminToRead":    true,
}

func guardCallName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	var name string
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	}
	if guardFuncs[name] {
		return name
	}
	return ""
}

func resolveStringExpr(expr ast.Expr, consts map[string]string) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		val, err := strconv.Unquote(node.Value)
		if err != nil {
			return "", false
		}
		return val, true
	case *ast.Ident:
		val, ok := consts[node.Name]
		return val, ok
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, okL := resolveStringExpr(node.X, consts)
		right, okR := resolveStringExpr(node.Y, consts)
		if !okL || !okR {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return resolveStringExpr(node.X, consts)
	}
	return "", false
}
