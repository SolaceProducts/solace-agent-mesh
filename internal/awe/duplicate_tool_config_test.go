// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"bytes"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

func TestRegisterTools_WarnsOnIgnoredDuplicateToolConfig(t *testing.T) {
	var buf bytes.Buffer
	originalDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	group := map[string]any{"tool_type": "builtin-group", "group_name": "data_analysis"}
	perTool := map[string]any{
		"tool_type":   "builtin",
		"tool_name":   "query_data_with_sql",
		"tool_config": map[string]any{"max_result_preview_rows": 2},
	}
	for _, tc := range []struct {
		name      string
		toolsCfg  []any
		wantWarns int
	}{
		{"per-tool entry after the group", []any{group, perTool}, 1},
		{"per-tool entry before the group", []any{perTool, group}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			svc := newTestServices()
			ag := agent.New(agent.AgentConfig{Name: "warn-agent"})
			if err := ag.Init(t.Context(), nil, svc); err != nil {
				t.Fatal(err)
			}
			if _, err := RegisterTools(ag, tc.toolsCfg, svc); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			out := buf.String()
			if got := strings.Count(out, "tool_config ignored"); got != tc.wantWarns {
				t.Fatalf("ignored tool_config warnings = %d, want %d; logs:\n%s", got, tc.wantWarns, out)
			}
			if tc.wantWarns > 0 {
				for _, attr := range []string{"level=WARN", "agentName=warn-agent", "tool=query_data_with_sql", "ignoredKeys=[max_result_preview_rows]"} {
					if !strings.Contains(out, attr) {
						t.Errorf("warning missing %q; logs:\n%s", attr, out)
					}
				}
			}
		})
	}
}

func TestIgnoredDuplicateToolConfigs(t *testing.T) {
	group := map[string]any{"tool_type": "builtin-group", "group_name": "data_analysis"}
	groupWithConfig := map[string]any{
		"tool_type":   "builtin-group",
		"group_name":  "data_analysis",
		"tool_config": map[string]any{"max_result_preview_bytes": 16384},
	}
	perTool := map[string]any{
		"tool_type":   "builtin",
		"tool_name":   "query_data_with_sql",
		"tool_config": map[string]any{"max_result_preview_rows": 2},
	}
	perToolNoConfig := map[string]any{"tool_type": "builtin", "tool_name": "query_data_with_sql"}

	tests := []struct {
		name      string
		toolsCfg  []any
		wantTools []string
		wantKeys  []string
	}{
		{"per-tool config after the group is ignored", []any{group, perTool}, []string{"query_data_with_sql"}, []string{"max_result_preview_rows"}},
		{"per-tool config before the group applies", []any{perTool, group}, nil, nil},
		{"a duplicate without tool_config is not reported", []any{group, perToolNoConfig}, nil, nil},
		{"group config after the per-tool entry is ignored for that tool", []any{perToolNoConfig, groupWithConfig}, []string{"query_data_with_sql"}, []string{"max_result_preview_bytes"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestServices()
			ag := agent.New(agent.AgentConfig{Name: "test-agent"})
			if err := ag.Init(t.Context(), nil, svc); err != nil {
				t.Fatal(err)
			}
			_, entries, err := toolinit.RegisterTools(agentToolinitHost{Agent: ag}, tc.toolsCfg, svc, agentToolinitOptions(svc))
			if err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			got := ignoredDuplicateToolConfigs(entries)
			var gotTools []string
			for _, ig := range got {
				gotTools = append(gotTools, ig.tool)
				if !slices.Equal(ig.keys, tc.wantKeys) {
					t.Errorf("ignored keys for %s = %v, want %v", ig.tool, ig.keys, tc.wantKeys)
				}
			}
			if !slices.Equal(gotTools, tc.wantTools) {
				t.Errorf("ignored tools = %v, want %v", gotTools, tc.wantTools)
			}
		})
	}
}
