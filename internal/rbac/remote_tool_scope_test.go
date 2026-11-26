// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac_test

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func TestFilterTools_ScopedRemoteTool_EndToEnd(t *testing.T) {
	scopedCfg, err := remote.ParseToolConfig(map[string]any{
		"tool_type":       "builtin",
		"tool_name":       "schedule_task",
		"required_scopes": []any{"schedule:task:create"},
	})
	if err != nil {
		t.Fatalf("ParseToolConfig(scoped tool): %v", err)
	}
	readCfg, err := remote.ParseToolConfig(map[string]any{
		"tool_type": "builtin",
		"tool_name": "query_task_health",
	})
	if err != nil {
		t.Fatalf("ParseToolConfig(read tool): %v", err)
	}
	scopedTool := remote.NewTool(scopedCfg, nil)
	readTool := remote.NewTool(readCfg, nil)

	infos := []auth.ToolInfo{
		{Name: scopedTool.Name(), RequiredScopes: scopedTool.RequiredScopes()},
		{Name: readTool.Name(), RequiredScopes: readTool.RequiredScopes()},
	}

	ta := rbac.NewToolAuth(nil, slog.Default())
	ctx := context.Background()
	visible := func(scopes []string) []string {
		allowed := ta.FilterTools(ctx, &auth.Identity{UserID: "u", Scopes: scopes}, infos)
		names := make([]string, 0, len(allowed))
		for _, ti := range allowed {
			names = append(names, ti.Name)
		}
		return names
	}

	tests := []struct {
		name         string
		scopes       []string
		wantScoped   bool
		wantReadTool bool
	}{
		{"with schedule scope sees both", []string{"schedule:task:create"}, true, true},
		{"unrelated read scope keeps read, loses scoped", []string{"analytics:_:read"}, false, true},
		{"schedule:* wildcard sees both", []string{"schedule:*"}, true, true},
		{"no scopes hides only the scoped tool", nil, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := visible(tc.scopes)
			if slices.Contains(got, "schedule_task") != tc.wantScoped {
				t.Errorf("schedule_task visible = %v, want %v (got %v)", slices.Contains(got, "schedule_task"), tc.wantScoped, got)
			}
			if slices.Contains(got, "query_task_health") != tc.wantReadTool {
				t.Errorf("query_task_health visible = %v, want %v (got %v)", slices.Contains(got, "query_task_health"), tc.wantReadTool, got)
			}
		})
	}
}
