// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var rendererDispatchers = []string{"buildConnectorToolEntries", "extractToolNamesFromConnector"}

var valuesMapNames = []string{"values", "Values", "vals"}

var callsThatReadNoKey = []string{"connectorPort", "connectorScheme", "delete", "withConnectorHIL"}

var valuesMapCopiers = []string{"eventMeshOperationValues", "Clone"}

type rendererSource struct {
	funcs      map[string]*ast.FuncDecl
	stringVars map[string]string
	valueNames map[string]map[string]bool
}

func parseRendererSource(t *testing.T, files map[string]string) *rendererSource {
	t.Helper()
	src := &rendererSource{
		funcs:      map[string]*ast.FuncDecl{},
		stringVars: map[string]string{},
		valueNames: map[string]map[string]bool{},
	}
	fset := token.NewFileSet()
	for name, text := range files {
		file, err := parser.ParseFile(fset, name, text, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Body != nil {
					src.funcs[d.Name.Name] = d
				}
			case *ast.GenDecl:
				src.collectStringConsts(d)
			}
			return true
		})
	}
	src.followValuesMap()
	return src
}

func (s *rendererSource) collectStringConsts(d *ast.GenDecl) {
	if d.Tok != token.CONST {
		return
	}
	for _, spec := range d.Specs {
		vs := spec.(*ast.ValueSpec)
		for i, name := range vs.Names {
			if i < len(vs.Values) {
				if value, ok := s.stringLiteral(vs.Values[i]); ok {
					s.stringVars[name.Name] = value
				}
			}
		}
	}
}

func (s *rendererSource) stringLiteral(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			value, err := strconv.Unquote(x.Value)
			return value, err == nil
		}
	case *ast.Ident:
		value, ok := s.stringVars[x.Name]
		return value, ok
	}
	return "", false
}

func (s *rendererSource) isValuesMap(fn string, e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return slices.Contains(valuesMapNames, x.Name) || s.valueNames[fn][x.Name]
	case *ast.SelectorExpr:
		return x.Sel.Name == "Values"
	case *ast.ParenExpr:
		return s.isValuesMap(fn, x.X)
	case *ast.StarExpr:
		return s.isValuesMap(fn, x.X)
	case *ast.CallExpr:
		return slices.Contains(valuesMapCopiers, calleeName(x)) && len(x.Args) == 1 && s.isValuesMap(fn, x.Args[0])
	}
	return false
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

func (s *rendererSource) markValuesMap(fn, name string) bool {
	if s.valueNames[fn] == nil {
		s.valueNames[fn] = map[string]bool{}
	}
	if s.valueNames[fn][name] {
		return false
	}
	s.valueNames[fn][name] = true
	return true
}

func (s *rendererSource) followValuesMap() {
	for changed := true; changed; {
		changed = false
		for fn, decl := range s.funcs {
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				changed = s.followNode(fn, n) || changed
				return true
			})
		}
	}
}

func (s *rendererSource) followNode(fn string, n ast.Node) bool {
	switch x := n.(type) {
	case *ast.AssignStmt:
		return s.followAssign(fn, x)
	case *ast.CallExpr:
		return s.followCall(fn, x)
	}
	return false
}

func (s *rendererSource) followAssign(fn string, assign *ast.AssignStmt) bool {
	if len(assign.Lhs) != len(assign.Rhs) {
		return false
	}
	changed := false
	for i, rhs := range assign.Rhs {
		if ident, ok := assign.Lhs[i].(*ast.Ident); ok && s.isValuesMap(fn, rhs) {
			changed = s.markValuesMap(fn, ident.Name) || changed
		}
	}
	return changed
}

func (s *rendererSource) followCall(caller string, call *ast.CallExpr) bool {
	callee, ok := call.Fun.(*ast.Ident)
	if !ok || s.funcs[callee.Name] == nil {
		return false
	}
	var params []*ast.Ident
	for _, field := range s.funcs[callee.Name].Type.Params.List {
		params = append(params, field.Names...)
	}
	changed := false
	for i, arg := range call.Args {
		if i < len(params) && s.isValuesMap(caller, arg) {
			changed = s.markValuesMap(callee.Name, params[i].Name) || changed
		}
	}
	return changed
}

func (s *rendererSource) keysRead(fn string, node ast.Node) []string {
	var keys []string
	written := map[ast.Expr]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range x.Lhs {
				written[lhs] = true
			}
		case *ast.IndexExpr:
			if key, ok := s.stringLiteral(x.Index); ok && s.isValuesMap(fn, x.X) && !written[x] {
				keys = append(keys, key)
			}
		case *ast.CallExpr:
			keys = append(keys, s.keysPassedToHelper(fn, x)...)
		}
		return true
	})
	return keys
}

func (s *rendererSource) keysPassedToHelper(fn string, call *ast.CallExpr) []string {
	if callee, ok := call.Fun.(*ast.Ident); ok && slices.Contains(callsThatReadNoKey, callee.Name) {
		return nil
	}
	var keys []string
	for i := 0; i+1 < len(call.Args); i++ {
		if key, ok := s.stringLiteral(call.Args[i+1]); ok && s.isValuesMap(fn, call.Args[i]) {
			keys = append(keys, key)
		}
	}
	return keys
}

func (s *rendererSource) callees(node ast.Node) []string {
	var names []string
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, ok := call.Fun.(*ast.Ident); ok && s.funcs[ident.Name] != nil {
				names = append(names, ident.Name)
			}
		}
		return true
	})
	return names
}

func (s *rendererSource) reachable(entries []string) map[string]bool {
	seen := map[string]bool{}
	queue := slices.Clone(entries)
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		if seen[fn] || s.funcs[fn] == nil {
			continue
		}
		seen[fn] = true
		queue = append(queue, s.callees(s.funcs[fn].Body)...)
	}
	return seen
}

func (s *rendererSource) keysByType(t *testing.T, dispatcher string) map[string]map[string][]string {
	t.Helper()
	decl := s.funcs[dispatcher]
	if decl == nil {
		t.Fatalf("dispatcher %s not found", dispatcher)
	}
	byType := map[string]map[string][]string{}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		if clause, ok := n.(*ast.CaseClause); ok {
			s.recordCase(byType, dispatcher, clause)
		}
		return true
	})
	return byType
}

func (s *rendererSource) recordCase(byType map[string]map[string][]string, dispatcher string, clause *ast.CaseClause) {
	body := &ast.BlockStmt{List: clause.Body}
	for _, e := range clause.List {
		connType, ok := s.stringLiteral(e)
		if !ok {
			continue
		}
		if byType[connType] == nil {
			byType[connType] = map[string][]string{}
		}
		recordKeys(byType[connType], dispatcher, s.keysRead(dispatcher, body))
		for fn := range s.reachable(s.callees(body)) {
			recordKeys(byType[connType], fn, s.keysRead(fn, s.funcs[fn].Body))
		}
	}
}

func recordKeys(readBy map[string][]string, fn string, keys []string) {
	for _, key := range keys {
		if !slices.Contains(readBy[key], fn) {
			readBy[key] = append(readBy[key], fn)
		}
	}
}

func packageSources(t *testing.T) map[string]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	files := map[string]string{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		files[path] = string(raw)
	}
	return files
}

func declaredFieldNamesByType(t *testing.T) map[string]map[string]bool {
	t.Helper()
	declared := map[string]map[string]bool{}
	for _, st := range schemaSubtypes(t) {
		if declared[st.connType] == nil {
			declared[st.connType] = map[string]bool{}
		}
		for _, f := range st.fields {
			declared[st.connType][f.Name] = true
		}
	}
	return declared
}

func keyIsAccepted(connType, key string, declared map[string]bool) bool {
	if declared[key] || slices.Contains(connectorRuntimeValueKeys[connType], key) {
		return true
	}
	return connType == "event_mesh" && slices.Contains(legacyEventMeshOperationKeys, key)
}

func TestConnectorRuntimeValueKeys_CoverEveryKeyTheRendererReads(t *testing.T) {
	src := parseRendererSource(t, packageSources(t))
	declaredByType := declaredFieldNamesByType(t)

	swept := 0
	for _, dispatcher := range rendererDispatchers {
		byType := src.keysByType(t, dispatcher)
		if dispatcher == rendererDispatchers[0] {
			for connType := range declaredByType {
				if byType[connType] == nil {
					t.Errorf("%s has no case for the declared connector type %s", dispatcher, connType)
				}
			}
		}
		swept += checkKeysAreAccepted(t, dispatcher, byType, declaredByType)
		if dispatcher == rendererDispatchers[0] {
			checkFlatEventMeshKeysAreRead(t, byType["event_mesh"])
			checkEveryTypeIsSwept(t, byType)
		}
	}
	if swept < minSweptRendererKeys {
		t.Fatalf("the sweep read only %d keys, want at least %d; it no longer follows the renderer", swept, minSweptRendererKeys)
	}
}

func checkKeysAreAccepted(t *testing.T, dispatcher string, byType map[string]map[string][]string, declaredByType map[string]map[string]bool) int {
	t.Helper()
	swept := 0
	for connType, keys := range byType {
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			swept++
			if !keyIsAccepted(connType, key, declaredByType[connType]) {
				t.Errorf("%s reads values[%q] for %s (in %s), which %s neither declares nor lists in connectorRuntimeValueKeys; a build would reject a connector that carries it",
					dispatcher, key, connType, strings.Join(keys[key], ", "), connType)
			}
		}
	}
	return swept
}

func checkFlatEventMeshKeysAreRead(t *testing.T, read map[string][]string) {
	t.Helper()
	for _, key := range legacyEventMeshOperationKeys {
		if _, ok := read[key]; !ok {
			t.Errorf("legacyEventMeshOperationKeys lists %q, which the event_mesh renderer no longer reads", key)
		}
	}
}

var minRendererKeysPerType = map[string]int{
	"sql": 9, "mcp": 23, "api": 17, "knowledge_base": 8, "graph_db": 14,
	"email": 21, "document_db": 17, "search": 15, "slack": 3, "event_mesh": 13,
}

func checkEveryTypeIsSwept(t *testing.T, byType map[string]map[string][]string) {
	t.Helper()
	for connType, minKeys := range minRendererKeysPerType {
		if got := len(byType[connType]); got < minKeys {
			t.Errorf("the sweep read %d keys for %s, want at least %d; it no longer follows that renderer", got, connType, minKeys)
		}
	}
}

const minSweptRendererKeys = 150

func TestRendererKeySweep_FollowsWhereTheKeysHide(t *testing.T) {
	const source = `package p

const keyFromConst = "const_key"

type conn struct{ Values map[string]any }

func buildConnectorToolEntries(c conn) []any {
	switch c.Type {
	case "t":
		return build(c)
	case "u":
		return nil
	}
	return nil
}

func build(c conn) []any {
	_ = c.Values["direct_key"]
	alias := c.Values
	_ = alias["alias_key"]
	_ = c.Values[keyFromConst]
	read(c.Values, "helper_key")
	forward(c.Values)
	viaCopy(c)
	connectorPort(c.Values, "not_a_key")
	return nil
}

func read(m map[string]any, key string) { _ = m[key] }

func viaCopy(c conn) {
	copied := eventMeshOperationValues(c.Values)
	_ = copied["copied_key"]
	cloned := maps.Clone(c.Values)
	_ = cloned["cloned_key"]
	cloned["written_key"] = 1
	delete(cloned, "deleted_key")
}

func forward(inner map[string]any) {
	_ = inner["forwarded_key"]
	deeper(inner)
}

func deeper(innermost map[string]any) { _ = innermost["deeper_key"] }

func unrelated(values map[string]any) { _ = values["not_reached_by_t"] }
`
	src := parseRendererSource(t, map[string]string{"p.go": source})
	byType := src.keysByType(t, "buildConnectorToolEntries")
	got := slices.Sorted(maps.Keys(byType["t"]))
	want := []string{"alias_key", "cloned_key", "const_key", "copied_key", "deeper_key", "direct_key", "forwarded_key", "helper_key"}
	if !slices.Equal(got, want) {
		t.Errorf("keys read for t = %v, want %v", got, want)
	}
	if len(byType["u"]) != 0 {
		t.Errorf("keys read for u = %v, want none", byType["u"])
	}
}
