// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"fmt"
	"go/ast"
	"go/types"
	"slices"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

type resolverRef struct {
	pkgSuffix string
	recv      string
	name      string
}

func (r resolverRef) String() string {
	if r.recv != "" {
		return r.pkgSuffix + "." + r.recv + "." + r.name
	}
	return r.pkgSuffix + "." + r.name
}

var identityResolvers = []resolverRef{
	{pkgSuffix: "/internal/entrypoint/httpsse", name: "resolveUserID"},
	{pkgSuffix: "/internal/entrypoint/auth", name: "UserIDFromRequest"},
	{pkgSuffix: "/internal/entrypoint/auth", name: "CallerFromRequest"},
	{pkgSuffix: "/internal/entrypoint/auth", name: "SubjectFromContext"},
	{pkgSuffix: "/internal/entrypoint/auth", name: "UserIDFromContext"},
	{pkgSuffix: "/internal/entrypoint", recv: "GatewayCore", name: "CanAccessAgent"},
}

var markersRequiringIdentity = map[string]bool{
	"publicReasonOwnerScoped":            true,
	"publicReasonShareScoped":            true,
	"publicReasonActivityScoped":         true,
	"publicReasonTraceReachScoped":       true,
	"publicReasonOwnerOrNamespaceScoped": true,
	"publicReasonCapabilityFiltered":     true,
	"publicReasonTaskTraceCovered":       true,
	"publicReasonFeedbackReadScoped":     true,
	"publicReasonFeedbackItemScoped":     true,
	"publicReasonFeedbackRollupScoped":   true,
}

var markersForbiddingIdentity = map[string]bool{
	"publicReasonNoUserData": true,
	"publicReasonAuthSkip":   true,
}

var markersDeliberatelyUnclassified = map[string]string{
	"publicReasonAuthenticatedOnly":    "grandfather: declares no route scope and claims nothing about the handler",
	"publicReasonGetNoScopeDeclared":   "grandfather: declares no route scope and claims nothing about the handler",
	"publicReasonTelemetryIngest":      "authenticated ingest of the caller's own events; no per-resource claim",
	"publicReasonTelemetryConsentRead": "install-wide decision, deliberately readable by every authenticated caller",
	"publicReasonAgentInvokeChecked":   "carried only by the /api/v2/chat route table, which TestChatV2PerCallerMarkersMatchTheGatewayFunction verifies",
}

var routesAppendedOutsideWireRoute = map[string]bool{
	"GET /api/v1/artifacts/scheduled/{id}/{filename}": true,
	"GET /api/v1/scheduledTasks/executions/{id}":      true,
	"DELETE /api/v1/scheduledTasks/executions/{id}":   true,
}

func TestRouteMarkersMatchHandlerBehaviour(t *testing.T) {
	perPass := make([]map[string]bool, len(conformanceBuildTags))
	t.Run("walks", func(t *testing.T) {
		for i, flags := range conformanceBuildTags {
			name := "default"
			if len(flags) > 0 {
				name = strings.Join(flags, ",")
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				perPass[i] = checkRouteMarkers(t, flags)
			})
		}
	})

	discovered := map[string]bool{}
	for _, seen := range perPass {
		for key := range seen {
			discovered[key] = true
		}
	}
	reconcileAgainstRegistry(t, discovered)
}

func checkRouteMarkers(t *testing.T, buildFlags []string) map[string]bool {
	p := loadConformancePkg(t, buildFlags)
	routes := p.routes()
	if len(routes) == 0 {
		t.Fatal("resolved 0 wireRoute declarations — the walk broke, not the routes")
	}

	perMarker := map[string]int{}
	discovered := map[string]bool{}
	var requiring, forbidding int
	for _, r := range routes {
		perMarker[r.marker]++
		discovered[r.method+" "+r.path] = true
		switch {
		case markersRequiringIdentity[r.marker]:
			requiring++
			if via := p.chainReachesResolver(r.chain); via == "" {
				t.Errorf("%s %s declares %s but nothing serving it reads the caller (checked %s)\n    declared at %s\n    either the handler is missing its ownership check, or the marker overstates what it does",
					r.method, r.path, r.marker, chainNames(r.chain), r.pos)
			}
		case markersForbiddingIdentity[r.marker]:
			forbidding++
			if via := p.chainReachesResolver(r.chain); via != "" {
				t.Errorf("%s %s declares %s — no identity resolved, nothing to authorize — but %s reaches %s\n    declared at %s\n    the response is caller-specific, so the marker is false and must change",
					r.method, r.path, r.marker, chainNames(r.chain), via, r.pos)
			}
		}
	}

	if requiring == 0 || forbidding == 0 {
		t.Errorf("checked %d per-caller and %d no-user-data routes; both must be non-zero or the marker names no longer match routes.go", requiring, forbidding)
	}
	for marker := range markersRequiringIdentity {
		if perMarker[marker] == 0 {
			t.Errorf("no route carries %s any more — either it was removed from routes.go, or the walk stopped matching it and the routes it covered are now unchecked", marker)
		}
	}
	for marker := range markersForbiddingIdentity {
		if perMarker[marker] == 0 {
			t.Errorf("no route carries %s any more — either it was removed from routes.go, or the walk stopped matching it and the routes it covered are now unchecked", marker)
		}
	}
	t.Logf("checked %d per-caller routes and %d no-user-data routes", requiring, forbidding)

	for _, msg := range p.unreadable {
		t.Errorf("the conformance walk could not read a route, so its marker is unverified: %s", msg)
	}

	for _, name := range p.markerConstants() {
		if markersRequiringIdentity[name] || markersForbiddingIdentity[name] {
			continue
		}
		if _, ok := markersDeliberatelyUnclassified[name]; ok {
			continue
		}
		t.Errorf("marker %s is declared but classified nowhere — add it to markersRequiringIdentity, markersForbiddingIdentity, or markersDeliberatelyUnclassified with the reason",
			name)
	}

	for _, want := range identityResolvers {
		if !p.resolverExists(want) {
			t.Errorf("identity resolver %s no longer exists — renamed or moved, so the checks depending on it are silently dead", want)
		}
	}
	if len(p.resolverSeen) == 0 {
		t.Error("no identity resolver was reached from any route — the call-graph walk is broken, not the routes")
	}
	return discovered
}

func reconcileAgainstRegistry(t *testing.T, discovered map[string]bool) {
	t.Helper()

	chatTable := map[string]bool{}
	for _, rt := range chatv2.Routes(chatv2.Deps{}) {
		reg := chatV2Registration(rt)
		chatTable[reg.Method+" "+reg.Path] = true
	}
	seenOutside := map[string]bool{}
	for _, r := range newCoverageAdapter(t).Routes() {
		if r.IsPublic == "" {
			continue
		}
		key := r.Method + " " + r.Path
		if chatTable[key] {
			continue
		}
		if routesAppendedOutsideWireRoute[key] {
			seenOutside[key] = true
			continue
		}
		if !discovered[key] {
			t.Errorf("%s is registered with marker %q but the conformance walk never saw it under any build configuration, so that claim is unverified — if it is appended to a.routes directly rather than wired through wireRoute, add it to routesAppendedOutsideWireRoute with the reason",
				key, r.IsPublic)
		}
	}
	for key := range routesAppendedOutsideWireRoute {
		if !seenOutside[key] {
			t.Errorf("routesAppendedOutsideWireRoute names %q, which is no longer registered — the carve-out is stale and would hide a real gap", key)
		}
	}
}

func (p *conformancePkg) markerConstants() []string {
	var out []string
	scope := p.pkg.Types.Scope()
	for _, name := range scope.Names() {
		if !strings.HasPrefix(name, "publicReason") {
			continue
		}
		if _, ok := scope.Lookup(name).(*types.Const); ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func chainNames(chain []*types.Func) string {
	out := make([]string, 0, len(chain))
	for _, fn := range chain {
		out = append(out, shortName(fn))
	}
	return strings.Join(out, ", ")
}

type conformanceRoute struct {
	method, path, marker, pos string
	chain                     []*types.Func
}

type conformancePkg struct {
	pkg          *packages.Package
	bodies       map[*types.Func]*ast.FuncDecl
	resolverSeen map[string]bool
	unreadable   []string
}

var conformanceBuildTags = [][]string{
	nil,
	{"-tags=product_analytics"},
}

func loadConformancePkg(t *testing.T, buildFlags []string) *conformancePkg {
	t.Helper()
	return loadConformancePkgAt(t, ".", buildFlags)
}

func loadConformancePkgAt(t *testing.T, pattern string, buildFlags []string) *conformancePkg {
	t.Helper()

	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
		Tests:      false,
		BuildFlags: buildFlags,
	}, pattern)
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d packages, want 1", len(loaded))
	}
	pkg := loaded[0]
	if len(pkg.Errors) > 0 {
		t.Fatalf("package has type errors, so resolution would be unreliable: %v", pkg.Errors[0])
	}

	p := &conformancePkg{pkg: pkg, bodies: map[*types.Func]*ast.FuncDecl{}, resolverSeen: map[string]bool{}}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func); ok {
				p.bodies[fn] = fd
			}
		}
	}
	return p
}

func (p *conformancePkg) routes() []conformanceRoute {
	var out []conformanceRoute
	for _, file := range p.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "wireRoute" || len(call.Args) < 3 {
				return true
			}
			pos := p.pkg.Fset.Position(call.Pos()).String()
			method, path, marker, scope := p.registrationFields(call.Args[1])
			if marker == "" && scope == "" {
				p.unreadable = append(p.unreadable,
					fmt.Sprintf("registration at %s: neither IsPublic nor Scope could be read", pos))
				return true
			}
			if marker == "" {
				return true
			}
			chain := p.resolveChain(call.Args[2])
			if len(chain) == 0 {
				p.unreadable = append(p.unreadable,
					fmt.Sprintf("%s %s at %s: the handler argument did not resolve to a function, so its %s claim is unverified",
						method, path, pos, marker))
				return true
			}
			out = append(out, conformanceRoute{
				method: method, path: path, marker: marker,
				chain: chain,
				pos:   pos,
			})
			return true
		})
	}
	p.recordUnfollowableWireRouteValues()
	return out
}

func (p *conformancePkg) recordUnfollowableWireRouteValues() {
	called := map[ast.Expr]bool{}
	var refs []*ast.SelectorExpr

	for _, file := range p.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "wireRoute" {
					called[sel] = true
				}
			case *ast.SelectorExpr:
				if v.Sel.Name == "wireRoute" {
					refs = append(refs, v)
				}
			}
			return true
		})
	}

	for _, ref := range refs {
		if called[ref] {
			continue
		}
		p.unreadable = append(p.unreadable, fmt.Sprintf(
			"wireRoute is used as a value at %s rather than called directly, so the routes registered through it are invisible to this walk — call it inline, or the markers on those routes are unverified",
			p.pkg.Fset.Position(ref.Pos())))
	}
}

func (p *conformancePkg) registrationLiteral(arg ast.Expr) *ast.CompositeLit {
	switch e := arg.(type) {
	case *ast.CompositeLit:
		return e
	case *ast.Ident:
		obj := p.pkg.TypesInfo.Uses[e]
		if obj == nil {
			return nil
		}
		var found *ast.CompositeLit
		for _, file := range p.pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				if found != nil {
					return false
				}
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, lhs := range as.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || i >= len(as.Rhs) {
						continue
					}
					if p.pkg.TypesInfo.Defs[id] != obj && p.pkg.TypesInfo.Uses[id] != obj {
						continue
					}
					if lit, ok := as.Rhs[i].(*ast.CompositeLit); ok {
						found = lit
						return false
					}
				}
				return true
			})
			if found != nil {
				return found
			}
		}
	}
	return nil
}

func (p *conformancePkg) registrationFields(arg ast.Expr) (method, path, marker, scope string) {
	lit := p.registrationLiteral(arg)
	if lit == nil {
		return "", "", "", ""
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Method":
			method = p.constString(kv.Value)
		case "Path":
			path = p.constString(kv.Value)
		case "IsPublic":
			if id, ok := kv.Value.(*ast.Ident); ok {
				marker = id.Name
			}
		case "Scope":
			scope = "set"
		}
	}
	return method, path, marker, scope
}

func (p *conformancePkg) constString(e ast.Expr) string {
	if tv, ok := p.pkg.TypesInfo.Types[e]; ok && tv.Value != nil {
		return strings.Trim(tv.Value.String(), `"`)
	}
	return ""
}

func (p *conformancePkg) resolveChain(arg ast.Expr) []*types.Func {
	switch v := arg.(type) {
	case *ast.SelectorExpr:
		if fn, ok := p.pkg.TypesInfo.Uses[v.Sel].(*types.Func); ok {
			return []*types.Func{fn}
		}
	case *ast.Ident:
		if fn, ok := p.pkg.TypesInfo.Uses[v].(*types.Func); ok {
			return []*types.Func{fn}
		}
	case *ast.CallExpr:
		var chain []*types.Func
		chain = append(chain, p.resolveChain(v.Fun)...)
		for _, a := range v.Args {
			chain = append(chain, p.resolveChain(a)...)
		}
		return chain
	}
	return nil
}

func (p *conformancePkg) chainReachesResolver(chain []*types.Func) string {
	for _, fn := range chain {
		if via := p.reachesResolver(fn); via != "" {
			return via
		}
	}
	return ""
}

func (p *conformancePkg) reachesResolver(fn *types.Func) string {
	return p.reaches(fn, func(callee *types.Func) bool {
		match, ok := matchResolver(callee)
		if ok {
			p.resolverSeen[match.String()] = true
		}
		return ok
	})
}

func (p *conformancePkg) reaches(fn *types.Func, isTarget func(*types.Func) bool) string {
	visited := map[*types.Func]bool{}

	var walk func(*types.Func) string
	walk = func(cur *types.Func) string {
		if cur == nil || visited[cur] {
			return ""
		}
		visited[cur] = true

		body, ok := p.bodies[cur]
		if !ok {
			return ""
		}

		var found string
		var callees []*types.Func
		ast.Inspect(body.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var id *ast.Ident
			switch f := call.Fun.(type) {
			case *ast.Ident:
				id = f
			case *ast.SelectorExpr:
				id = f.Sel
			default:
				return true
			}
			callee, ok := p.pkg.TypesInfo.Uses[id].(*types.Func)
			if !ok {
				return true
			}
			if isTarget(callee) {
				if found == "" {
					found = shortName(callee)
				}
				return false
			}
			callees = append(callees, callee)
			return true
		})
		if found != "" {
			return found
		}
		for _, c := range callees {
			if via := walk(c); via != "" {
				return via
			}
		}
		return ""
	}
	return walk(fn)
}

func matchResolver(fn *types.Func) (resolverRef, bool) {
	if fn.Pkg() == nil {
		return resolverRef{}, false
	}
	pkgPath := fn.Pkg().Path()
	recv := receiverTypeName(fn)
	for _, want := range identityResolvers {
		if fn.Name() == want.name && strings.HasSuffix(pkgPath, want.pkgSuffix) && recv == want.recv {
			return want, true
		}
	}
	return resolverRef{}, false
}

func receiverTypeName(fn *types.Func) string {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return ""
	}
	t := sig.Recv().Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	if named, ok := t.(*types.Named); ok {
		return named.Obj().Name()
	}
	return ""
}

func (p *conformancePkg) resolverExists(want resolverRef) bool {
	scopes := map[string]*types.Scope{}
	if p.pkg.Types != nil {
		scopes[p.pkg.PkgPath] = p.pkg.Types.Scope()
	}
	for _, imp := range p.pkg.Imports {
		if imp.Types != nil {
			scopes[imp.PkgPath] = imp.Types.Scope()
		}
	}
	for path, scope := range scopes {
		if !strings.HasSuffix(path, want.pkgSuffix) {
			continue
		}
		if want.recv == "" {
			if _, ok := scope.Lookup(want.name).(*types.Func); ok {
				return true
			}
			continue
		}
		obj := scope.Lookup(want.recv)
		if obj == nil {
			continue
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			continue
		}
		for i := 0; i < named.NumMethods(); i++ {
			if named.Method(i).Name() == want.name {
				return true
			}
		}
	}
	return false
}

func shortName(fn *types.Func) string {
	full := fn.FullName()
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

func TestConformanceWalk_ClosureHandlerIsUnreadable(t *testing.T) {
	p := loadConformancePkgAt(t, "./testdata/closureroute", nil)
	routes := p.routes()

	discovered := map[string]bool{}
	for _, r := range routes {
		discovered[r.path] = true
	}
	if !discovered["/fixture/named"] {
		t.Error("the named-method control did not resolve, so this fixture proves nothing about closures specifically")
	}

	for _, path := range []string{"/fixture/closure", "/fixture/closure-nouserdata"} {
		if discovered[path] {
			t.Errorf("%s resolved to a function — a closure must not, or its marker is checked against a body the walk invented", path)
		}
		if !slices.ContainsFunc(p.unreadable, func(msg string) bool { return strings.Contains(msg, path) }) {
			t.Errorf("%s produced no unreadable finding, so a closure-wired route is silently treated as conforming; findings were: %v",
				path, p.unreadable)
		}
	}
}

func TestConformanceWalk_WireRouteAsValueIsUnreadable(t *testing.T) {
	p := loadConformancePkgAt(t, "./testdata/closureroute", nil)

	for _, r := range p.routes() {
		if r.path == "/fixture/via-method-value" {
			t.Fatal("the walk resolved a route registered through a method value — if that now works, this test and recordUnfollowableWireRouteValues are stale")
		}
	}

	if !slices.ContainsFunc(p.unreadable, func(msg string) bool {
		return strings.Contains(msg, "wireRoute is used as a value")
	}) {
		t.Errorf("wireRoute taken as a value produced no finding, so routes registered through it are silently unverified; findings were: %v", p.unreadable)
	}
}
