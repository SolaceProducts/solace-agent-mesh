// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import "testing"

func TestInvokeScopeForInstance(t *testing.T) {
	const uuid = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
	const underscored = "019e47f4_2be0_75d8_828e_77a0d1d9221f"

	tests := []struct {
		name     string
		cardName string
		want     string
	}{
		{
			name:     "deployed workflow card resolves to workflow scope with dashed uuid",
			cardName: "workflow_" + underscored,
			want:     "workflow:" + uuid + ":invoke",
		},
		{
			name:     "platform agent card resolves to agent scope with dashed uuid",
			cardName: "agent_" + underscored,
			want:     "agent:" + uuid + ":invoke",
		},
		{
			name:     "plain YAML agent name keeps plain agent scope",
			cardName: "OrchestratorAgent",
			want:     "agent:OrchestratorAgent:invoke",
		},
		{
			name:     "prefixed but non-uuid body falls back to plain agent scope",
			cardName: "agent_not-a-uuid",
			want:     "agent:agent_not-a-uuid:invoke",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InvokeScopeForInstance(tt.cardName); got != tt.want {
				t.Errorf("InvokeScopeForInstance(%q) = %q, want %q", tt.cardName, got, tt.want)
			}
		})
	}
}
