// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApprovalRule_Evaluate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule ApprovalRule
		args map[string]any
		want bool
	}{
		{"eq string match", ApprovalRule{Arg: "method", Op: OpEq, Value: "POST"}, map[string]any{"method": "POST"}, true},
		{"eq string mismatch", ApprovalRule{Arg: "method", Op: OpEq, Value: "POST"}, map[string]any{"method": "GET"}, false},
		{"eq missing arg", ApprovalRule{Arg: "method", Op: OpEq, Value: "POST"}, map[string]any{}, false},
		{"eq bool true", ApprovalRule{Arg: "force", Op: OpEq, Value: true}, map[string]any{"force": true}, true},
		{"eq bool false", ApprovalRule{Arg: "force", Op: OpEq, Value: false}, map[string]any{"force": true}, false},
		{"eq number int vs float", ApprovalRule{Arg: "n", Op: OpEq, Value: 5}, map[string]any{"n": float64(5)}, true},
		{"eq number mismatch", ApprovalRule{Arg: "n", Op: OpEq, Value: 5}, map[string]any{"n": float64(7)}, false},
		{"eq type mismatch string vs number", ApprovalRule{Arg: "n", Op: OpEq, Value: 5}, map[string]any{"n": "5"}, false},

		{"in match", ApprovalRule{Arg: "method", Op: OpIn, Value: []any{"POST", "PUT", "DELETE"}}, map[string]any{"method": "PUT"}, true},
		{"in no match", ApprovalRule{Arg: "method", Op: OpIn, Value: []any{"POST", "PUT", "DELETE"}}, map[string]any{"method": "GET"}, false},
		{"in missing arg", ApprovalRule{Arg: "method", Op: OpIn, Value: []any{"POST"}}, map[string]any{}, false},
		{"not_in match returns true (not present in list)", ApprovalRule{Arg: "method", Op: OpNotIn, Value: []any{"POST"}}, map[string]any{"method": "GET"}, true},
		{"not_in no match returns false (present in list)", ApprovalRule{Arg: "method", Op: OpNotIn, Value: []any{"POST"}}, map[string]any{"method": "POST"}, false},
		{"not_in missing arg fail-open", ApprovalRule{Arg: "method", Op: OpNotIn, Value: []any{"POST"}}, map[string]any{}, false},

		{"exists present", ApprovalRule{Arg: "confirmation_token", Op: OpExists}, map[string]any{"confirmation_token": "abc"}, true},
		{"exists missing", ApprovalRule{Arg: "confirmation_token", Op: OpExists}, map[string]any{}, false},
		{"exists nil value still counts as present", ApprovalRule{Arg: "x", Op: OpExists}, map[string]any{"x": nil}, true},
		{"not_exists missing", ApprovalRule{Arg: "confirmation_token", Op: OpNotExists}, map[string]any{}, true},
		{"not_exists present", ApprovalRule{Arg: "confirmation_token", Op: OpNotExists}, map[string]any{"confirmation_token": "abc"}, false},

		{"gt match", ApprovalRule{Arg: "amount", Op: OpGt, Value: 100}, map[string]any{"amount": float64(150)}, true},
		{"gt boundary not strict", ApprovalRule{Arg: "amount", Op: OpGt, Value: 100}, map[string]any{"amount": float64(100)}, false},
		{"gt no match", ApprovalRule{Arg: "amount", Op: OpGt, Value: 100}, map[string]any{"amount": float64(50)}, false},
		{"gt non-numeric arg fail-open", ApprovalRule{Arg: "amount", Op: OpGt, Value: 100}, map[string]any{"amount": "lots"}, false},
		{"gt missing arg fail-open", ApprovalRule{Arg: "amount", Op: OpGt, Value: 100}, map[string]any{}, false},
		{"lt match", ApprovalRule{Arg: "amount", Op: OpLt, Value: 100}, map[string]any{"amount": float64(50)}, true},
		{"lt boundary not strict", ApprovalRule{Arg: "amount", Op: OpLt, Value: 100}, map[string]any{"amount": float64(100)}, false},
		{"gte at boundary", ApprovalRule{Arg: "amount", Op: OpGte, Value: 100}, map[string]any{"amount": float64(100)}, true},
		{"gte above", ApprovalRule{Arg: "amount", Op: OpGte, Value: 100}, map[string]any{"amount": float64(150)}, true},
		{"gte below", ApprovalRule{Arg: "amount", Op: OpGte, Value: 100}, map[string]any{"amount": float64(50)}, false},
		{"lte at boundary", ApprovalRule{Arg: "amount", Op: OpLte, Value: 100}, map[string]any{"amount": float64(100)}, true},
		{"lte above", ApprovalRule{Arg: "amount", Op: OpLte, Value: 100}, map[string]any{"amount": float64(150)}, false},

		{"nested map match", ApprovalRule{Arg: "body.priority", Op: OpEq, Value: "critical"}, map[string]any{"body": map[string]any{"priority": "critical"}}, true},
		{"nested map miss leaf", ApprovalRule{Arg: "body.priority", Op: OpEq, Value: "critical"}, map[string]any{"body": map[string]any{"priority": "low"}}, false},
		{"nested map missing intermediate", ApprovalRule{Arg: "body.priority", Op: OpEq, Value: "critical"}, map[string]any{"other": "x"}, false},
		{"nested non-map intermediate fails", ApprovalRule{Arg: "body.priority", Op: OpEq, Value: "critical"}, map[string]any{"body": "stringnotmap"}, false},
		{"deep nested match", ApprovalRule{Arg: "a.b.c", Op: OpExists}, map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}}, true},
		{"deep nested missing", ApprovalRule{Arg: "a.b.c", Op: OpExists}, map[string]any{"a": map[string]any{"b": map[string]any{}}}, false},

		{"in numeric coercion", ApprovalRule{Arg: "code", Op: OpIn, Value: []any{200, 201}}, map[string]any{"code": float64(201)}, true},

		{"empty path returns false", ApprovalRule{Arg: "", Op: OpExists}, map[string]any{"x": 1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.rule.Evaluate(tc.args)
			if got != tc.want {
				t.Errorf("Evaluate(%+v, %+v) = %v, want %v", tc.rule, tc.args, got, tc.want)
			}
		})
	}
}

func TestApprovalRule_Evaluate_JSONNumber(t *testing.T) {
	t.Parallel()
	dec := json.NewDecoder(strings.NewReader(`{"amount": 250}`))
	dec.UseNumber()
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rule := ApprovalRule{Arg: "amount", Op: OpGt, Value: 100}
	if !rule.Evaluate(args) {
		t.Errorf("expected gt to match json.Number value")
	}
}

func TestHILToolConfig_ShouldGate(t *testing.T) {
	t.Parallel()

	t.Run("require_approval=true gates regardless of rules", func(t *testing.T) {
		t.Parallel()
		cfg := HILToolConfig{RequireApproval: true}
		if !cfg.ShouldGate(nil) {
			t.Error("expected gate when RequireApproval=true and args=nil")
		}
	})

	t.Run("no rules and require_approval=false does not gate", func(t *testing.T) {
		t.Parallel()
		cfg := HILToolConfig{}
		if cfg.ShouldGate(map[string]any{"method": "POST"}) {
			t.Error("expected no gate when no rules and RequireApproval=false")
		}
	})

	t.Run("rule matches and RequireApproval=false still gates", func(t *testing.T) {
		t.Parallel()
		cfg := HILToolConfig{
			ApprovalWhen: []ApprovalRule{
				{Arg: "method", Op: OpIn, Value: []any{"POST", "PUT"}},
			},
		}
		if !cfg.ShouldGate(map[string]any{"method": "POST"}) {
			t.Error("expected gate when rule matches")
		}
		if cfg.ShouldGate(map[string]any{"method": "GET"}) {
			t.Error("expected no gate when no rule matches")
		}
	})

	t.Run("RequireApproval=true OR matching rule", func(t *testing.T) {
		t.Parallel()
		cfg := HILToolConfig{
			RequireApproval: true,
			ApprovalWhen: []ApprovalRule{
				{Arg: "method", Op: OpEq, Value: "POST"},
			},
		}
		if !cfg.ShouldGate(map[string]any{"method": "GET"}) {
			t.Error("expected gate via RequireApproval even when rule does not match")
		}
	})

	t.Run("any matching rule causes gate (OR across rules)", func(t *testing.T) {
		t.Parallel()
		cfg := HILToolConfig{
			ApprovalWhen: []ApprovalRule{
				{Arg: "method", Op: OpEq, Value: "DELETE"},
				{Arg: "confirmation_token", Op: OpNotExists},
			},
		}
		if !cfg.ShouldGate(map[string]any{"method": "GET"}) {
			t.Error("expected gate via second rule")
		}
		if cfg.ShouldGate(map[string]any{"method": "GET", "confirmation_token": "abc"}) {
			t.Error("expected no gate when no rule matches")
		}
	})
}
