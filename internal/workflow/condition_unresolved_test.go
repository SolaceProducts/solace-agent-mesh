// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"strings"
	"testing"
)

func presentNodeOutputs() map[string]map[string]any {
	return map[string]map[string]any{
		"find_user": {"output": map[string]any{
			"status":  "success",
			"message": `MCP tool "find_user" returned empty response`,
		}},
	}
}

func TestEvaluateCondition_MissingFieldOnPresentNode_IsUnresolved(t *testing.T) {
	for name, cond := range map[string]string{
		"equality":   `'{{find_user.output.mcp_structured_content}}' == '{}'`,
		"inequality": `'{{find_user.output.mcp_structured_content}}' != '{}'`,
		"unquoted":   `{{find_user.output.mcp_structured_content}} == 0`,
	} {
		t.Run(name, func(t *testing.T) {
			matched, err := EvaluateCondition(cond, presentNodeOutputs())
			if !errors.Is(err, ErrUnresolvedReference) {
				t.Fatalf("err = %v, want ErrUnresolvedReference", err)
			}
			if matched {
				t.Error("matched = true; an unresolved reference must never select a branch")
			}
			if !strings.Contains(err.Error(), "mcp_structured_content") {
				t.Errorf("error %q does not name the field that failed", err)
			}
		})
	}
}

func TestEvaluateCondition_MissingNodeStaysLenient(t *testing.T) {
	matched, err := EvaluateCondition(`'{{absent.output.val}}' == '{{absent.output.val}}'`, presentNodeOutputs())
	if err != nil {
		t.Fatalf("unexpected error for an unknown node: %v", err)
	}
	if !matched {
		t.Error("matched = false; two identical unresolved literals still compare equal")
	}
}

func TestEvaluateCondition_PresentFieldStillResolves(t *testing.T) {
	matched, err := EvaluateCondition(`'{{find_user.output.status}}' == 'success'`, presentNodeOutputs())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("matched = false, want true")
	}
}

func TestEvaluateCondition_EmptyStructuredContentIsGateable(t *testing.T) {
	outputs := map[string]map[string]any{
		"find_user": {"output": map[string]any{
			"mcp_structured_content": map[string]any{},
			"status":                 "processed",
		}},
	}
	matched, err := EvaluateCondition(`'{{find_user.output.mcp_structured_content}}' == '{}'`, outputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !matched {
		t.Error("matched = false; an empty structured result must be branchable")
	}
}

func TestEvaluateCondition_KeepsOtherOutcomesDistinct(t *testing.T) {
	if matched, err := EvaluateCondition("", presentNodeOutputs()); err != nil || !matched {
		t.Errorf("empty condition = (%v, %v), want (true, nil)", matched, err)
	}

	_, err := EvaluateCondition(`'{{find_user.output.status}}' ==== 'x'`, presentNodeOutputs())
	if err == nil {
		t.Fatal("a malformed expression must still error")
	}
	if errors.Is(err, ErrUnresolvedReference) {
		t.Error("a malformed expression was misreported as an unresolved reference")
	}
}
