// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

type toolDispatchType struct {
	pkgPath, typeName, spendField string
}

var toolDispatchTypes = []toolDispatchType{
	{modulePath + "/internal/tool", "Context", "RemoteSpend"},
	{modulePath + "/internal/tool/remote", "InvokeRequest", "Spend"},
}

var unattributedToolDispatches = map[string]llmCallSite{
	"internal/agent/task_loop.go:Agent.setupTask": {1, "wireInternalLLMCallbacks installs BindToolCall, " +
		"which sets RemoteSpend on each dispatched call's context"},
	"internal/agent/task_loop.go:Agent.buildToolCtxFromState": {1, "wireInternalLLMCallbacks installs " +
		"BindToolCall, which sets RemoteSpend on each dispatched call's context"},
	"internal/entrypoint/agentlink/peers.go:peerCallContext": {1, "builds the context agent.PeerRequest and " +
		"PeerRequestProperties read; dispatches no tool"},
	"internal/agent/remote_tool_resume.go:Agent.callerOfPausedTask": {1, "builds a context only to read the paused " +
		"task's caller for an audit event; it runs no tool"},
	"internal/agent/remote_tool_resume.go:Agent.endCancelledWaitsOfStoredTask": {1, "builds a context only to read the " +
		"cancelled task's caller for audit events and publishes an STR cancel; it runs no tool"},
	"internal/agent/tool_approval_resume.go:Agent.endCancelledApprovals": {1, "builds a context only to audit " +
		"approvals that ended in cancellation; their calls never run"},
	"internal/agent/tool_auth_manage.go:ListToolAuthInfo":                {1, "lists credential status; runs no tool"},
	"internal/agent/tool_auth_manage.go:BeginToolAuthFlow":               {1, "starts a credential flow; runs no tool"},
	"internal/agent/tool_auth_manage.go:DisconnectToolAuthCredential":    {1, "clears a credential; runs no tool"},
	"internal/agent/agent.go:Agent.handleAuthResponse":                   {1, "completes a tool's auth exchange; runs no tool"},
	"internal/workflow/tool_auth.go:WorkflowInstance.handleAuthResponse": {1, "completes a tool's auth exchange; runs no tool"},
	"internal/awe/skills.go:strSkillLoader.LoadSkill":                    {1, "load_skill reads a skill bundle; it calls no LLM"},
	"internal/notify/connector.go:executorInvoker.Invoke":                {1, "notification connectors deliver a message; they call no LLM"},
	"internal/indexing/pdfremote/remote.go:Extractor.Extract":            {1, "the PDF extractor returns page text; it calls no LLM"},
	"internal/identity/enricher_str.go:STREnricher.invoke": {1, "identity enrichment toolsets look up a profile; " +
		"none shipped calls an LLM. One that does needs a system user to bill its spend to"},
	"cmd/str-bench/main.go:run": {1, "the STR benchmark dev CLI; its calls are not SAM's spend"},
}

func TestEveryToolDispatchForwardsItsSpendAttribution(t *testing.T) {
	found := scannedToolDispatches(t)
	var unlisted []string
	for site, dispatches := range found {
		listed, ok := unattributedToolDispatches[site]
		switch {
		case !ok:
			unlisted = append(unlisted, site)
		case listed.constructions != dispatches:
			t.Errorf("%s builds %d tool dispatches without their spend field; unattributedToolDispatches lists %d (%s). "+
				"Attribute any new dispatch, then update the count and reason.",
				site, dispatches, listed.constructions, listed.reason)
		}
	}
	slices.Sort(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("tool dispatches are built without a spend attribution in:\n  %s\n\n"+
			"The STR books the LLM calls a tool makes only when the dispatch carries tool.Context.RemoteSpend or "+
			"remote.InvokeRequest.Spend; without it they are dropped. Set it (agent.RemoteToolSpend), or add the "+
			"function to unattributedToolDispatches with the reason the tool makes no LLM call.",
			strings.Join(unlisted, "\n  "))
	}
}

func TestUnattributedToolDispatchAllowlistHasNoStaleEntries(t *testing.T) {
	found := scannedToolDispatches(t)
	internal := sourcetree.IsInternal(t)
	for site, listed := range unattributedToolDispatches {
		file, _, _ := strings.Cut(site, ":")
		if _, err := os.Stat(filepath.Join("..", "..", file)); errors.Is(err, fs.ErrNotExist) && !internal {
			continue
		}
		if found[site] == 0 {
			t.Errorf("listed %q (%s) no longer builds an unattributed tool dispatch; remove the entry", site, listed.reason)
		}
	}
}

var scanToolDispatchesOnce = sync.OnceValues(func() (map[string]int, error) {
	return scanFirstPartySource(filepath.Join("..", ".."), unattributedDispatches)
})

func scannedToolDispatches(t *testing.T) map[string]int {
	t.Helper()
	sites, err := scanToolDispatchesOnce()
	if err != nil {
		t.Fatal(err)
	}
	return sites
}

func unattributedDispatches(path, rel string) (map[string]int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	ownPackage := modulePath + "/" + filepath.ToSlash(filepath.Dir(rel))
	imported, err := importNames(file, rel, func(importPath string) bool {
		return slices.ContainsFunc(toolDispatchTypes, func(dt toolDispatchType) bool { return dt.pkgPath == importPath })
	})
	if err != nil {
		return nil, err
	}
	dispatchType := func(expr ast.Expr) (toolDispatchType, bool) {
		pkgPath, typeName := ownPackage, ""
		switch expr := expr.(type) {
		case *ast.Ident:
			typeName = expr.Name
		case *ast.SelectorExpr:
			pkg, isIdent := expr.X.(*ast.Ident)
			if !isIdent {
				return toolDispatchType{}, false
			}
			pkgPath, typeName = imported[pkg.Name], expr.Sel.Name
		}
		for _, dt := range toolDispatchTypes {
			if dt.pkgPath == pkgPath && dt.typeName == typeName {
				return dt, true
			}
		}
		return toolDispatchType{}, false
	}
	setsField := func(lit *ast.CompositeLit, field string) bool {
		return slices.ContainsFunc(lit.Elts, func(elt ast.Expr) bool {
			kv, isKV := elt.(*ast.KeyValueExpr)
			if !isKV {
				return false
			}
			key, isIdent := kv.Key.(*ast.Ident)
			if !isIdent || key.Name != field {
				return false
			}
			value, isIdent := kv.Value.(*ast.Ident)
			return !isIdent || value.Name != "nil"
		})
	}

	callers := map[string]int{}
	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if !isFunc || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, isLit := n.(*ast.CompositeLit)
			if !isLit {
				return true
			}
			if dt, ok := dispatchType(lit.Type); ok && !setsField(lit, dt.spendField) {
				callers[funcName(fn)]++
			}
			dt, ok := dispatchType(elementType(lit.Type))
			if !ok {
				return true
			}
			for _, elided := range elidedElements(lit) {
				if !setsField(elided, dt.spendField) {
					callers[funcName(fn)]++
				}
			}
			return true
		})
	}
	return callers, nil
}

func elementType(expr ast.Expr) ast.Expr {
	var elem ast.Expr
	switch expr := expr.(type) {
	case *ast.ArrayType:
		elem = expr.Elt
	case *ast.MapType:
		elem = expr.Value
	}
	if star, isStar := elem.(*ast.StarExpr); isStar {
		return star.X
	}
	return elem
}

func elidedElements(lit *ast.CompositeLit) []*ast.CompositeLit {
	var elided []*ast.CompositeLit
	for _, elt := range lit.Elts {
		if kv, isKV := elt.(*ast.KeyValueExpr); isKV {
			elt = kv.Value
		}
		if inner, isLit := elt.(*ast.CompositeLit); isLit && inner.Type == nil {
			elided = append(elided, inner)
		}
	}
	return elided
}

func TestUnattributedDispatchesCountsADispatchWhoseSpendIsNil(t *testing.T) {
	path := writeGuardFixture(t, `package fixture

import (
	"`+modulePath+`/internal/tool"
	"`+modulePath+`/internal/tool/remote"
)

func attributed(spend *remotetool.SpendAttribution) {
	_ = &tool.Context{RemoteSpend: spend}
	_ = remote.InvokeRequest{Spend: spend}
}

func unattributed() {
	_ = &tool.Context{}
	_ = &tool.Context{RemoteSpend: nil}
	_ = remote.InvokeRequest{Spend: nil}
}

func elided(spend *remotetool.SpendAttribution) {
	_ = []tool.Context{{RemoteSpend: spend}, {}}
	_ = []*tool.Context{{}}
	_ = map[string]remote.InvokeRequest{"a": {}}
}
`)
	got, err := unattributedDispatches(path, "internal/fixture/fixture.go")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"unattributed": 3, "elided": 3}; !maps.Equal(got, want) {
		t.Errorf("unattributedDispatches = %v, want %v", got, want)
	}
}

func TestUnattributedDispatchesRejectsADotImportOfADispatchPackage(t *testing.T) {
	path := writeGuardFixture(t, `package fixture

import . "`+modulePath+`/internal/tool"

func dispatch() { _ = &Context{} }
`)
	if _, err := unattributedDispatches(path, "internal/fixture/fixture.go"); err == nil {
		t.Error("a dot-import of internal/tool was accepted; it would hide the dispatch from the guard")
	}
}

func writeGuardFixture(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
