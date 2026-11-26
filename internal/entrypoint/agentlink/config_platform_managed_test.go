// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	platformAgentName    = "agent_0190f3c2_7a52_7c11_9d3e_5b6f1a2c3d4e"
	platformWorkflowName = "workflow_0190f3c2_7a52_7c11_9d3e_5b6f1a2c3d4e"
	someTokenDigest      = "abababababababababababababababababababababababababababababababab"
)

func TestParseConfig_PlatformManagedAccepts(t *testing.T) {
	tests := []struct {
		name       string
		raw        map[string]any
		wantAgents []string
	}{
		{
			name:       "an empty agents list",
			raw:        map[string]any{"platform_managed": true, "agents": []any{}},
			wantAgents: nil,
		},
		{
			name:       "no agents key",
			raw:        map[string]any{"platform_managed": true},
			wantAgents: nil,
		},
		{
			name: "an agent under the name the platform mints for it",
			raw: map[string]any{"platform_managed": true, "agents": []any{
				map[string]any{"name": platformAgentName, "token_sha256": someTokenDigest},
			}},
			wantAgents: []string{platformAgentName},
		},
		{
			name: "an agent under a name an operator chose",
			raw: map[string]any{"platform_managed": true, "agents": []any{
				map[string]any{"name": "claims-assistant", "token_sha256": someTokenDigest},
			}},
			wantAgents: []string{"claims-assistant"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(tt.raw)
			if err != nil {
				t.Fatalf("ParseConfig(%v) = %v, want it accepted", tt.raw, err)
			}
			if !cfg.PlatformManaged {
				t.Errorf("ParseConfig(%v).PlatformManaged = false, want true", tt.raw)
			}
			if diff := cmp.Diff(tt.wantAgents, slices.Sorted(maps.Keys(cfg.Agents))); diff != "" {
				t.Errorf("ParseConfig(%v) agents mismatch (-want +got):\n%s", tt.raw, diff)
			}
		})
	}
}

func TestParseConfig_Refuses(t *testing.T) {
	tests := []struct {
		name       string
		raw        map[string]any
		wantReason string
	}{
		{
			name:       "an empty agents list without platform_managed",
			raw:        map[string]any{"agents": []any{}},
			wantReason: "agents: at least one agent is required",
		},
		{
			name:       "an empty agents list with platform_managed false",
			raw:        map[string]any{"platform_managed": false, "agents": []any{}},
			wantReason: "agents: at least one agent is required",
		},
		{
			name: "a platform-minted agent name without platform_managed",
			raw: map[string]any{"agents": []any{
				map[string]any{"name": platformAgentName, "token_sha256": someTokenDigest},
			}},
			wantReason: "reserved for agents and workflows the platform deploys",
		},
		{
			name: "a platform-minted workflow name with platform_managed",
			raw: map[string]any{"platform_managed": true, "agents": []any{
				map[string]any{"name": platformWorkflowName, "token_sha256": someTokenDigest},
			}},
			wantReason: "reserved for agents and workflows the platform deploys",
		},
		{
			name: "platform_managed that is not a boolean",
			raw: map[string]any{"platform_managed": "true", "agents": []any{
				map[string]any{"name": "claims-assistant", "token_sha256": someTokenDigest},
			}},
			wantReason: "platform_managed: must be true or false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(tt.raw)
			if err == nil {
				t.Fatalf("ParseConfig(%v) = %+v, want it refused", tt.raw, cfg)
			}
			if !strings.Contains(err.Error(), tt.wantReason) {
				t.Errorf("ParseConfig(%v) = %q, want a refusal saying %q", tt.raw, err, tt.wantReason)
			}
		})
	}
}
