// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestLookupBuiltinTool_DataToolsHonourOwnToolConfig(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"query_data_with_sql", map[string]any{
			"input_files": map[string]any{"items": "items.json"},
			"sql_query":   "SELECT id FROM items ORDER BY id",
		}},
		{"transform_data_with_jmespath", map[string]any{
			"input_filename":      "items.json",
			"jmespath_expression": "[*]",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toolMap := map[string]any{
				"tool_type":   "builtin",
				"tool_name":   c.name,
				"tool_config": map[string]any{"max_result_preview_rows": 2},
			}
			bt := LookupBuiltinTool(c.name, toolMap, Options{})
			if bt == nil {
				t.Fatalf("LookupBuiltinTool(%q) returned nil", c.name)
			}

			ctx := t.Context()
			toolCtx := &tool.Context{
				ArtifactService: &artifact.ScopedService{Inner: memory.New(), Scope: "agent"},
				AgentName:       "test-agent",
				UserID:          "test-user",
				SessionID:       "test-session",
			}
			items := []byte(`[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5}]`)
			if _, err := toolCtx.Facade().SaveArtifact(ctx, "items.json", items, "application/json"); err != nil {
				t.Fatalf("save artifact: %v", err)
			}

			result, err := bt.Execute(ctx, c.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Status != "success" {
				t.Fatalf("status = %q (%s), want success", result.Status, result.Message)
			}
			var previewRows int
			switch p := result.Data["result_preview"].(type) {
			case []any:
				previewRows = len(p)
			case []map[string]any:
				previewRows = len(p)
			default:
				t.Fatalf("result_preview is %T, want a list", p)
			}
			if previewRows != 2 {
				t.Errorf("preview rows = %d, want 2 from tool_config.max_result_preview_rows", previewRows)
			}
			if result.Data["result_truncated"] != true {
				t.Errorf("result_truncated = %v, want true", result.Data["result_truncated"])
			}
		})
	}
}

func TestLookupBuiltinTool_CreateSQLiteDBHonoursOwnMaxColumns(t *testing.T) {
	toolMap := map[string]any{
		"tool_type":   "builtin",
		"tool_name":   "create_sqlite_db",
		"tool_config": map[string]any{"max_columns": 2},
	}
	bt := LookupBuiltinTool("create_sqlite_db", toolMap, Options{})
	if bt == nil {
		t.Fatal("LookupBuiltinTool(create_sqlite_db) returned nil")
	}

	ctx := t.Context()
	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memory.New(), Scope: "agent"},
		AgentName:       "test-agent",
		UserID:          "test-user",
		SessionID:       "test-session",
	}
	wide := []byte(`[{"id":1,"name":"a","arr":10},{"id":2,"name":"b","arr":20}]`)
	if _, err := toolCtx.Facade().SaveArtifact(ctx, "wide.json", wide, "application/json"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	result, err := bt.Execute(ctx, map[string]any{
		"input_filename":     "wide.json",
		"output_db_filename": "wide.sqlite",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != "error" || !strings.Contains(result.Message, "maximum of 2") {
		t.Errorf("result = %q (%s), want an error citing tool_config.max_columns 2", result.Status, result.Message)
	}
}
