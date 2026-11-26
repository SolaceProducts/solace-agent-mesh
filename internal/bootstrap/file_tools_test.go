// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

func builtinGroups(tools []any) []string {
	var groups []string
	for _, entry := range tools {
		if m, ok := entry.(map[string]any); ok && m["tool_type"] == toolinit.ToolTypeBuiltinGroup {
			groups = append(groups, m["group_name"].(string))
		}
	}
	return groups
}

func TestEveryAgentGetsTheFileToolsUnlessItOptsOut(t *testing.T) {
	off := false
	declared := []any{map[string]any{"tool_type": "builtin", "tool_name": "execute_python_code"}}
	cases := []struct {
		name  string
		tools []any
		cfg   agent.AgentConfig
		want  int
	}{
		{"an agent with no tools of its own", nil, agent.AgentConfig{Name: "writer"}, 2},
		{"an agent with tools of its own", declared, agent.AgentConfig{Name: "analyst"}, 2},
		{"an agent that opts out of both", nil, agent.AgentConfig{Name: "lean", AutoInjectArtifactTools: &off, AutoInjectDataAnalysisTools: &off}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := builtinGroups(withFileTools(tc.tools, &tc.cfg, nil)); len(got) != tc.want {
				t.Errorf("builtin groups = %v, want %d", got, tc.want)
			}
		})
	}
}
