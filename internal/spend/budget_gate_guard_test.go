// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var recordingConstructors = []string{"NewRecordingClient", "NewRecordingEvaluator"}

func TestEveryRecordedLLMCallIsGated(t *testing.T) {
	var ungated []string
	err := walkFirstPartySource(filepath.Join("..", ".."), func(path, rel string) error {
		if filepath.Dir(rel) == "internal/spend" {
			return nil
		}
		found, scanErr := recorderCalls(path, rel)
		ungated = append(ungated, found...)
		return scanErr
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ungated)
	if len(ungated) > 0 {
		t.Errorf("spend recorders built without a budget gate:\n  %s\n\n"+
			"Build each with spend.NewMeteredClient or spend.NewMeteredEvaluator, with the process's budget gate, "+
			"so the call is refused while the budget is spent.",
			strings.Join(ungated, "\n  "))
	}
}

func recorderCalls(path, rel string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	spendName := ""
	for _, spec := range file.Imports {
		if importPath, _ := strconv.Unquote(spec.Path.Value); importPath == modulePath+"/internal/spend" {
			spendName = "spend"
			if spec.Name != nil {
				spendName = spec.Name.Name
			}
		}
	}
	if spendName == "" {
		return nil, nil
	}
	var calls []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !slices.Contains(recordingConstructors, sel.Sel.Name) {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == spendName {
			calls = append(calls, fmt.Sprintf("%s:%d %s", rel, fset.Position(sel.Pos()).Line, sel.Sel.Name))
		}
		return true
	})
	return calls, nil
}
