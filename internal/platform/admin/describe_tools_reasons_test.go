// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestDescribeToolReasons_ListsEveryReasonConstantAndNothingElse(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "describe_tools_reasons.go", nil, 0)
	if err != nil {
		t.Fatalf("parse describe_tools_reasons.go: %v", err)
	}
	var declared []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "reason") || i >= len(spec.Values) {
				continue
			}
			literal, isString := spec.Values[i].(*ast.BasicLit)
			if !isString || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", name.Name, err)
			}
			declared = append(declared, value)
		}
		return true
	})
	if len(declared) == 0 {
		t.Fatal("found no reason* string constants: the sweep is looking in the wrong place")
	}

	listed := DescribeToolReasons()
	for _, value := range declared {
		if !slices.Contains(listed, value) {
			t.Errorf("a reason constant holds %q, which DescribeToolReasons() does not list", value)
		}
	}
	for _, value := range listed {
		if !slices.Contains(declared, value) {
			t.Errorf("DescribeToolReasons() lists %q, which no reason constant holds", value)
		}
	}
	if len(listed) != len(declared) {
		t.Errorf("DescribeToolReasons() lists %d reasons, %d constants declare them", len(listed), len(declared))
	}
}
