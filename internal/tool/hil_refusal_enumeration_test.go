// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

func TestAllApprovalRefusals_MatchesTheConstantBlock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "hil.go", nil, 0)
	if err != nil {
		t.Fatalf("parse hil.go: %v", err)
	}

	var declared []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		ident, ok := spec.Type.(*ast.Ident)
		if !ok || ident.Name != "ApprovalRefusal" {
			return true
		}
		for _, name := range spec.Names {
			declared = append(declared, name.Name)
		}
		return true
	})
	if len(declared) == 0 {
		t.Fatal("found no ApprovalRefusal constants; this test is not reading what it thinks it is")
	}

	enumerated := map[ApprovalRefusal]bool{}
	for _, r := range AllApprovalRefusals() {
		enumerated[r] = true
	}
	byName := map[string]ApprovalRefusal{
		"RefusalDenied":             RefusalDenied,
		"RefusalTimedOut":           RefusalTimedOut,
		"RefusalCancelled":          RefusalCancelled,
		"RefusalUnreadable":         RefusalUnreadable,
		"RefusalUnexpected":         RefusalUnexpected,
		"RefusalRunEnded":           RefusalRunEnded,
		"RefusalRunEndedUnanswered": RefusalRunEndedUnanswered,
		"RefusalNoApprovalRoute":    RefusalNoApprovalRoute,
		"RefusalExitHandler":        RefusalExitHandler,
		"RefusalPromptFailed":       RefusalPromptFailed,
		"RefusalBackstop":           RefusalBackstop,
		"RefusalNoHumanInEvalRun":   RefusalNoHumanInEvalRun,
	}

	slices.Sort(declared)
	for _, name := range declared {
		value, known := byName[name]
		if !known {
			t.Errorf("constant %s is new: add it to AllApprovalRefusals, classify it in the audit logger, and add it to this test's name map", name)
			continue
		}
		if !enumerated[value] {
			t.Errorf("constant %s is declared but missing from AllApprovalRefusals, so nothing holds the audit logger to classifying it", name)
		}
	}
	if len(declared) != len(AllApprovalRefusals()) {
		t.Errorf("AllApprovalRefusals has %d entries for %d declared constants", len(AllApprovalRefusals()), len(declared))
	}
}
