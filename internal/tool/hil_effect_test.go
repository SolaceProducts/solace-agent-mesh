// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"slices"
	"testing"
)

func TestShouldGateCall_AToolsEffectNeverChangesWhetherItIsGated(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cfg  HILToolConfig
		args map[string]any
		want bool
	}{
		{"require_approval", HILToolConfig{RequireApproval: true}, map[string]any{"amount": 1}, true},
		{"a matching rule", HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "amount", Op: OpGt, Value: 5}}}, map[string]any{"amount": 9}, true},
		{"a rule that does not match", HILToolConfig{ApprovalWhen: []ApprovalRule{{Arg: "amount", Op: OpGt, Value: 5}}}, map[string]any{"amount": 1}, false},
		{"no gate configured", HILToolConfig{}, map[string]any{"amount": 9}, false},
	} {
		for _, class := range []EffectClass{DeclaredEffect(EffectReadOnly), DeclaredEffect(EffectSideEffecting), DefaultEffectClass} {
			t.Run(tc.name+"/"+string(class.Effect)+"/"+string(class.Source), func(t *testing.T) {
				t.Parallel()
				subject := &classifiedTool{mockTool: newMockTool("subject"), class: class}
				if got := ShouldGateCall(subject, tc.cfg, tc.args); got != tc.want {
					t.Errorf("ShouldGateCall for a %s/%s tool = %v, want %v", class.Effect, class.Source, got, tc.want)
				}
			})
		}
	}
}

func TestParseEffectAndHIL_AnEntryMayCarryBothAnEffectAndApprovers(t *testing.T) {
	t.Parallel()
	entry := HILEntry{
		ToolMap: map[string]any{
			"tool_type": "builtin", "tool_name": "reader",
			EffectConfigKey: "side_effecting",
			"hil": map[string]any{
				"require_approval": true,
				"approvers":        map[string]any{"users": []any{"alice@example.com"}},
			},
		},
		ToolNames: []string{"reader"},
	}

	overrides, err := ParseEffectOverrides(nil, []HILEntry{entry})
	if err != nil {
		t.Fatalf("ParseEffectOverrides: %v", err)
	}
	if want := (EffectClass{Effect: EffectSideEffecting, Source: EffectSourceOperator}); overrides["reader"] != want {
		t.Errorf("override for reader = %+v, want %+v", overrides["reader"], want)
	}

	configs, err := ParseHILConfigs(nil, []HILEntry{entry})
	if err != nil {
		t.Fatalf("ParseHILConfigs: %v", err)
	}
	cfg, gated := configs["reader"]
	if !gated || !cfg.RequireApproval || !slices.Equal(cfg.Approvers.Users, []string{"alice@example.com"}) {
		t.Errorf("hil config for reader = %+v (gated %v), want require_approval with approver alice@example.com", cfg, gated)
	}
}
