// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import "testing"

func TestInstanceNameRoundTrip(t *testing.T) {
	const id = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
	tests := []struct {
		kind ComponentKind
		want string
	}{
		{KindAgent, "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f"},
		{KindWorkflow, "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"},
	}
	for _, tt := range tests {
		name := InstanceName(tt.kind, id)
		if name != tt.want {
			t.Errorf("InstanceName(%q, %q) = %q, want %q", tt.kind, id, name, tt.want)
		}
		gotID, gotKind, ok := IDFromInstanceName(name)
		if !ok || gotID != id || gotKind != tt.kind {
			t.Errorf("IDFromInstanceName(%q) = (%q, %q, %v), want (%q, %q, true)", name, gotID, gotKind, ok, id, tt.kind)
		}
	}
}

func TestIDFromInstanceName_Invalid(t *testing.T) {
	cases := []string{
		"OrchestratorAgent",
		"agent_not-a-uuid",
		"agent_",
		"gateway_019e47f4",
		"",
	}
	for _, name := range cases {
		if id, kind, ok := IDFromInstanceName(name); ok {
			t.Errorf("IDFromInstanceName(%q) = (%q, %q, true), want ok=false", name, id, kind)
		}
	}
}

func TestCanonicalComponentID(t *testing.T) {
	cases := map[string]string{
		"agent_019e47f4_2be0_75d8_828e_77a0d1d9221f":    "019e47f4-2be0-75d8-828e-77a0d1d9221f",
		"workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f": "019e47f4-2be0-75d8-828e-77a0d1d9221f",
		"OrchestratorAgent":                             "OrchestratorAgent",
		"agent_not-a-uuid":                              "agent_not-a-uuid",
	}
	for name, want := range cases {
		if got := CanonicalComponentID(name); got != want {
			t.Errorf("CanonicalComponentID(%q) = %q, want %q", name, got, want)
		}
	}
}
