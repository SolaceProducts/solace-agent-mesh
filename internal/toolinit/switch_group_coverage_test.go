// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func TestEveryGroupInTheSwitchIsReachable(t *testing.T) {
	labels := switchCaseLabels(t, "builtin.go", "LookupBuiltinGroup")
	if len(labels) == 0 {
		t.Fatal("parsed no case labels from LookupBuiltinGroup — the parser lost the switch, " +
			"so this guard proves nothing")
	}

	for _, group := range labels {
		if _, inCatalog := schema.ValidBuiltinGroups[group]; inCatalog {
			continue
		}
		if slices.Contains(switchOnlyGroups, group) {
			continue
		}
		t.Errorf("LookupBuiltinGroup resolves %q, which is in neither schema.ValidBuiltinGroups "+
			"nor switchOnlyGroups — every guard in this package walks one of those two, so this "+
			"group is checked by none of them. Add it to the catalog, or to switchOnlyGroups if "+
			"it is deliberately absent from the builder.", group)
	}
}

func switchCaseLabels(t *testing.T, filename, funcName string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}

	var labels []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					labels = append(labels, lit.Value[1:len(lit.Value)-1])
				}
			}
			return true
		})
	}
	return labels
}
