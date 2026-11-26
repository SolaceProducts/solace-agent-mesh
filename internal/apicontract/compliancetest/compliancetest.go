// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package compliancetest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type KeyPathVar struct{ Path, Name string }

type Config struct {
	Scopes               map[string]bool
	Gates                map[apicontract.Gate]string
	KeyPathVars          map[KeyPathVar]string
	OpenObjects          map[string]string
	Results              map[reflect.Type]bool
	LegacyTimestamp      reflect.Type
	LegacyTimestampUsers map[reflect.Type]bool
	WirePackage          string
	MountFunc            string
}

var (
	camelCaseRE   = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	pathVarRE     = regexp.MustCompile(`^\{([^}]*)\}$`)
	toggleParamRE = regexp.MustCompile(`^(fetch|include|with|show)[A-Z]`)
	listParams    = map[string]bool{
		"page": true, "perPage": true, "skip": true, "limit": true, "offset": true,
		"pageNumber": true, "pageSize": true, "sort": true,
	}
	allowedMethod = map[string]bool{
		http.MethodGet: true, http.MethodPost: true, http.MethodPut: true,
		http.MethodPatch: true, http.MethodDelete: true,
	}
)

func Check(t *testing.T, cfg Config, routes []apicontract.Route) {
	t.Helper()
	if len(routes) == 0 {
		t.Fatal("the route table is empty, so the route rules would pass without checking anything")
	}
	resources, requests := WireTypes(routes)
	typeProblems, reached := TypeViolations(cfg, resources, requests)
	fset, files := ParseDir(t, ".")

	for _, rule := range []struct {
		name     string
		problems []string
	}{
		{"Routes", RouteViolations(cfg, routes)},
		{"RouteDocs", DocViolations(routes)},
		{"WireTypes", typeProblems},
		{"EveryWireTypeReached", UnreachedWireStructs(files, reached)},
		{"OnlyTheMountMountsRoutes", MuxCallsOutsideMount(fset, files, cfg.MountFunc)},
	} {
		t.Run(rule.name, func(t *testing.T) {
			if len(rule.problems) > 0 {
				t.Errorf("%d violation(s):\n%s", len(rule.problems), strings.Join(rule.problems, "\n"))
			}
		})
	}
}

func RouteViolations(cfg Config, routes []apicontract.Route) []string {
	if cfg.Scopes == nil {
		cfg.Scopes = catalogScopes()
	}
	var out []string
	seen := map[string]bool{}
	for _, rt := range routes {
		op := rt.Method + " " + rt.Path
		bad := func(format string, args ...any) { out = append(out, op+": "+fmt.Sprintf(format, args...)) }

		if !allowedMethod[rt.Method] {
			bad("method must be GET, POST, PUT, PATCH or DELETE")
		}
		gates := 0
		for _, set := range []bool{rt.Scope != "", len(rt.AnyOfScopes) > 0, rt.Gate != ""} {
			if set {
				gates++
			}
		}
		switch {
		case gates == 0:
			bad("no gate: set one of Scope, AnyOfScopes or Gate")
		case gates > 1:
			bad("set one of Scope, AnyOfScopes or Gate")
		case rt.Gate != "":
			text, known := cfg.Gates[rt.Gate]
			switch {
			case !known:
				bad("Gate %q is not a known gate", rt.Gate)
			case text == "":
				bad("Gate %q has no entry in gateDocs, so the reference cannot say what it requires", rt.Gate)
			}
		case len(rt.AnyOfScopes) == 1:
			bad("AnyOfScopes holds one scope; use Scope")
		}
		for _, scope := range rt.Scopes() {
			if !cfg.Scopes[scope] {
				bad("scope %q is not in the RBAC catalog", scope)
			}
		}
		for _, problem := range handlerViolations(cfg, rt) {
			bad("%s", problem)
		}
		if seen[op] {
			bad("registered twice")
		}
		seen[op] = true
		for _, problem := range PathViolations(cfg, rt.Path, textPathVars(rt.Handler)) {
			bad("%s", problem)
		}
	}
	return out
}

var (
	summaryVerb = regexp.MustCompile(`^(Get |Upload |Create |Update |Delete |Replace |Initiate |Validate |Add or replace |Add an association between |Remove an association between )`)
	tagName     = regexp.MustCompile(`^[A-Z][a-zA-Z-]*( [A-Z][a-zA-Z-]*)*$`)
)

func DocViolations(routes []apicontract.Route) []string {
	var out []string
	ids := map[string]string{}
	for _, rt := range routes {
		op := rt.Method + " " + rt.Path
		bad := func(format string, args ...any) { out = append(out, op+": "+fmt.Sprintf(format, args...)) }

		if rt.Stability != apicontract.StabilityDraft && rt.Stability != apicontract.StabilityStable {
			bad("Stability must be StabilityDraft or StabilityStable")
		}
		if rt.Doc.ID == "" || rt.Doc.Summary == "" || rt.Doc.Text == "" || rt.Doc.Tag.Name == "" {
			bad("Doc needs an ID, Summary, Text and Tag.Name")
		}
		if rt.Doc.Summary != "" && !summaryVerb.MatchString(rt.Doc.Summary) {
			bad("Summary %q must start with Get, Upload, Create, Update, Delete, Replace, Initiate, Validate, Add or replace, Add an association between or Remove an association between", rt.Doc.Summary)
		}
		if rt.Doc.Tag.Group == "" {
			bad("Tag %q needs a Group", rt.Doc.Tag.Name)
		}
		if rt.Doc.Tag.Name != "" && !tagName.MatchString(rt.Doc.Tag.Name) {
			bad("Tag.Name %q must be words that each start with a capital letter, such as \"Evaluation Runs\"", rt.Doc.Tag.Name)
		}
		if other, ok := ids[rt.Doc.ID]; ok && rt.Doc.ID != "" {
			bad("Doc.ID %q is also %s", rt.Doc.ID, other)
		}
		ids[rt.Doc.ID] = op
		if rt.AllowAPIKey && rt.Stability != apicontract.StabilityStable {
			bad("a route that sets AllowAPIKey is promised to callers, so it must be StabilityStable")
		}
		if rt.Published && rt.Stability == apicontract.StabilityDraft {
			bad("a published route cannot be StabilityDraft")
		}
	}
	return out
}

func handlerViolations(cfg Config, rt apicontract.Route) []string {
	h := rt.Handler
	if !h.Bound() {
		return []string{"no handler; build one with handle"}
	}
	var out []string
	b := apicontract.BindingOf(h.Request)
	bound := make([]string, 0, len(b.Path))
	for _, p := range b.Path {
		bound = append(bound, p.Name)
		if cfg.KeyPathVars[KeyPathVar{rt.Path, p.Name}] != "" && !p.Key {
			out = append(out, fmt.Sprintf("path variable %q addresses a built-in key, so tag it path:\"%s,key\" or path:\"%s,foldedkey\"", p.Name, p.Name, p.Name))
		}
	}
	if vars := pathVars(rt.Path); !slices.Equal(slices.Sorted(slices.Values(bound)), vars) {
		out = append(out, fmt.Sprintf("%s binds path variables %v, the path has %v", h.Request, bound, vars))
	}
	for _, q := range b.Query {
		switch {
		case !camelCaseRE.MatchString(q.Name):
			out = append(out, fmt.Sprintf("query param %q must be camelCase", q.Name))
		case listParams[q.Name]:
			out = append(out, fmt.Sprintf("query param %q: read paging into a page and ?sort into a sortOrder", q.Name))
		case toggleParamRE.MatchString(q.Name):
			out = append(out, fmt.Sprintf("query param %q is a toggle; name optional fields in ?expand", q.Name))
		}
	}
	if rt.Method == http.MethodDelete && h.Response != reflect.TypeFor[struct{}]() {
		out = append(out, "a DELETE answers 204, so its handler must return struct{}")
	}
	if apicontract.IsList(h.Response) && !b.HasPage {
		out = append(out, fmt.Sprintf("%s returns a list, so %s needs a page field", h.Response, h.Request))
	}
	return out
}

func pathVars(path string) []string {
	vars := []string{}
	for seg := range strings.SplitSeq(path, "/") {
		if m := pathVarRE.FindStringSubmatch(seg); m != nil {
			vars = append(vars, strings.TrimSuffix(m[1], "..."))
		}
	}
	slices.Sort(vars)
	return vars
}

func textPathVars(h apicontract.Handler) map[string]bool {
	vars := map[string]bool{}
	if !h.Bound() {
		return vars
	}
	for _, p := range apicontract.BindingOf(h.Request).Path {
		if p.Text {
			vars[p.Name] = true
		}
	}
	return vars
}

func WireTypes(routes []apicontract.Route) (resources, requests []reflect.Type) {
	for _, rt := range routes {
		h := rt.Handler
		if !h.Bound() {
			continue
		}
		requests = append(requests, h.Request)
		switch {
		case h.Wire == nil:
		case h.Resource != nil:
			resources = append(resources, h.Resource)
		case apicontract.IsList(h.Response):
			items, _ := h.Response.FieldByName("Items")
			resources = append(resources, items.Type.Elem())
		default:
			data, _ := h.Wire.FieldByName("Data")
			if data.Type.Kind() == reflect.Pointer {
				resources = append(resources, data.Type.Elem())
			} else {
				resources = append(resources, data.Type)
			}
		}
	}
	return resources, requests
}

func PathViolations(cfg Config, path string, text map[string]bool) []string {
	if !strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return []string{"path must start with / and not end with one"}
	}
	if strings.HasPrefix(path, "/api/") {
		return []string{"path is below the prefix; drop /api/..."}
	}
	var out []string
	segments := strings.Split(path[1:], "/")
	lastVar := -1
	for i, seg := range segments {
		if pathVarRE.MatchString(seg) {
			lastVar = i
		}
	}
	for i, seg := range segments {
		m := pathVarRE.FindStringSubmatch(seg)
		if m == nil {
			if !camelCaseRE.MatchString(seg) {
				out = append(out, fmt.Sprintf("segment %q is not camelCase", seg))
			}
			continue
		}
		name, wildcard := strings.CutSuffix(m[1], "...")
		switch {
		case text[name]:
			if !camelCaseRE.MatchString(name) {
				out = append(out, fmt.Sprintf("path variable %q must be camelCase", name))
			}
			if wildcard && i != len(segments)-1 {
				out = append(out, fmt.Sprintf("only the last path variable may be a wildcard, not %q", name))
			}
		case wildcard:
			out = append(out, fmt.Sprintf("path variable %q is a wildcard; read it with path:%q", name, name+",text"))
		case name == "id" && i != lastVar:
			out = append(out, "only the last path variable may be {id}; name a parent {<type>Id}")
		case !camelCaseRE.MatchString(name):
			out = append(out, fmt.Sprintf("path variable %q must be camelCase", name))
		case cfg.KeyPathVars[KeyPathVar{path, name}] != "":
		case name != "id" && !strings.HasSuffix(name, "Id"):
			out = append(out, fmt.Sprintf("path variable %q must be {id} or {<type>Id}", name))
		}
	}
	return out
}

func TypeViolations(cfg Config, resources, requests []reflect.Type) ([]string, map[string]bool) {
	w := &typeWalker{cfg: cfg, seen: map[reflect.Type]bool{}, reached: map[string]bool{}, response: true}
	for _, t := range resources {
		names := jsonNames(t)
		if !cfg.Results[t] && (!names["id"] || !names["type"]) {
			w.bad(t.String(), "a resource must carry id and type")
		}
		w.walk(t, t.String())
	}
	w.response = false
	for _, t := range requests {
		w.walk(t, t.String())
	}
	return w.out, w.reached
}

type typeWalker struct {
	cfg      Config
	out      []string
	seen     map[reflect.Type]bool
	reached  map[string]bool
	response bool
}

func (w *typeWalker) bad(where, problem string) {
	w.out = append(w.out, where+": "+problem)
}

func (w *typeWalker) walk(t reflect.Type, where string) {
	switch {
	case t == apicontract.InstantType:
		return
	case isPatch(t):
		w.walk(patchValue(t), where)
		return
	case t == reflect.TypeFor[time.Time]() || t.Name() == "Timestamp":
		w.bad(where, "send a time as Instant")
		return
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		w.walk(t.Elem(), where)
	case reflect.Interface:
		if w.cfg.OpenObjects[where] == "" {
			w.bad(where, "an interface or untyped map has no schema; give it a type")
		}
	case reflect.Struct:
		if w.seen[t] {
			return
		}
		w.seen[t] = true
		if t.PkgPath() == w.cfg.WirePackage {
			w.reached[t.Name()] = true
		}
		w.walkFields(t)
	}
}

func (w *typeWalker) walkFields(t reflect.Type) {
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || f.Anonymous {
			continue
		}
		name, tagged := jsonName(f)
		where := typeLabel(t) + "." + name
		switch {
		case !tagged:
			w.bad(typeLabel(t)+"."+f.Name, "exported field needs a json tag")
		case name == "-":
			continue
		case !camelCaseRE.MatchString(name):
			w.bad(where, "json name must be camelCase")
		case strings.HasSuffix(name, "Time") && !isInstantField(f.Type) && !w.legacyTimestamp(t, f.Type):
			w.bad(where, "a ...Time field must be an Instant")
		}
		if w.response && omitsEmpty(f) && !isPatch(f.Type) {
			w.bad(where, "a response field is always sent; drop omitempty and send null")
		}
		for _, v := range oneofValues(f) {
			if !apicontract.LowerSnakeCase.MatchString(v) {
				w.bad(where, fmt.Sprintf("enum value %q must be lowercase snake_case", v))
			}
		}
		w.walk(f.Type, where)
	}
}

func (w *typeWalker) legacyTimestamp(owner, ft reflect.Type) bool {
	if w.cfg.LegacyTimestamp == nil || !w.cfg.LegacyTimestampUsers[owner] {
		return false
	}
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	return ft == w.cfg.LegacyTimestamp
}

func omitsEmpty(f reflect.StructField) bool {
	_, options, _ := strings.Cut(f.Tag.Get("json"), ",")
	for option := range strings.SplitSeq(options, ",") {
		if option == "omitempty" || option == "omitzero" {
			return true
		}
	}
	return false
}

func oneofValues(f reflect.StructField) []string {
	for rule := range strings.SplitSeq(f.Tag.Get("validate"), ",") {
		if values, ok := strings.CutPrefix(rule, "oneof="); ok {
			return strings.Fields(values)
		}
	}
	return nil
}

func isPatch(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.PkgPath() == apicontract.InstantType.PkgPath() && strings.HasPrefix(t.Name(), "Patch[")
}

func patchValue(t reflect.Type) reflect.Type {
	f, _ := t.FieldByName("Value")
	return f.Type
}

func isInstantField(t reflect.Type) bool {
	if isPatch(t) {
		t = patchValue(t)
	}
	return t == apicontract.InstantType || (t.Kind() == reflect.Pointer && t.Elem() == apicontract.InstantType)
}

func jsonName(f reflect.StructField) (string, bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, false
	}
	name, _, _ := strings.Cut(tag, ",")
	return name, name != ""
}

func jsonNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}
	for _, f := range reflect.VisibleFields(t) {
		if name, ok := jsonName(f); ok {
			names[name] = true
		}
	}
	return names
}

func typeLabel(t reflect.Type) string {
	if t.Name() != "" {
		return t.Name()
	}
	return "struct"
}

func ParseDir(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(abs, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files = append(files, f)
	}
	return fset, files
}

func UnreachedWireStructs(files []*ast.File, reached map[string]bool) []string {
	var out []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok || spec.TypeParams != nil {
				return true
			}
			st, ok := spec.Type.(*ast.StructType)
			if ok && hasJSONTag(st) && !reached[spec.Name.Name] {
				out = append(out, spec.Name.Name)
			}
			return true
		})
	}
	return out
}

func hasJSONTag(st *ast.StructType) bool {
	for _, field := range st.Fields.List {
		if field.Tag != nil && strings.Contains(field.Tag.Value, `json:"`) {
			return true
		}
	}
	return false
}

func MuxCallsOutsideMount(fset *token.FileSet, files []*ast.File, mountFunc string) []string {
	var out []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name == mountFunc {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Handle" || sel.Sel.Name == "HandleFunc") && !isPackageIdent(sel.X, "apicontract") {
					out = append(out, fmt.Sprintf("%s in %s", fset.Position(call.Pos()), fn.Name.Name))
				}
				return true
			})
		}
	}
	return out
}

func isPackageIdent(x ast.Expr, pkg string) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == pkg
}

func catalogScopes() map[string]bool {
	scopes := map[string]bool{}
	for _, s := range rbac.Catalog() {
		scopes[s.Scope] = true
	}
	return scopes
}
