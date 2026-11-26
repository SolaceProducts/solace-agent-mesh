// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	previewTestAccounts      = 10
	previewTestFitAtDefault  = 6
	previewTestRoomyCapBytes = 16384
)

func previewTestAccountsJSON(t *testing.T) []byte {
	t.Helper()
	accounts := make([]map[string]any, previewTestAccounts)
	for i := range accounts {
		accounts[i] = map[string]any{
			"id":    fmt.Sprintf("acc-%02d", i),
			"name":  fmt.Sprintf("Account %02d", i),
			"arr":   100000 + i,
			"csm":   fmt.Sprintf("csm-%02d", i),
			"notes": strings.Repeat("n", 230),
		}
	}
	data, err := json.Marshal(accounts)
	if err != nil {
		t.Fatalf("marshal accounts: %v", err)
	}
	return data
}

type previewToolCase struct {
	name    string
	newTool func(config map[string]any) tool.BuiltinTool
	args    map[string]any
}

func previewToolCases() []previewToolCase {
	return []previewToolCase{
		{
			name:    "query_data_with_sql",
			newTool: func(c map[string]any) tool.BuiltinTool { return NewQueryDataWithSQLTool(c) },
			args: map[string]any{
				"input_files":   map[string]any{"accounts": "accounts.json"},
				"sql_query":     "SELECT id, name, arr, csm, notes FROM accounts ORDER BY id",
				"output_format": "json",
			},
		},
		{
			name:    "transform_data_with_jmespath",
			newTool: func(c map[string]any) tool.BuiltinTool { return NewTransformDataWithJMESPathTool(c) },
			args: map[string]any{
				"input_filename":      "accounts.json",
				"jmespath_expression": "[*]",
			},
		},
	}
}

func previewLen(t *testing.T, preview any) int {
	t.Helper()
	switch p := preview.(type) {
	case []any:
		return len(p)
	case []map[string]any:
		return len(p)
	default:
		t.Fatalf("result_preview is %T, want a list", preview)
		return 0
	}
}

func TestDataToolPreview_ByteCap(t *testing.T) {
	roomy := map[string]any{"max_result_preview_bytes": previewTestRoomyCapBytes}
	tests := []struct {
		name          string
		ownConfig     map[string]any
		callConfig    map[string]any
		wantRows      int
		wantTruncated bool
	}{
		{"default cap keeps the longest fitting prefix", nil, nil, previewTestFitAtDefault, true},
		{"per-call tool_config raises the cap", nil, roomy, previewTestAccounts, false},
		{"own tool_config (YAML int) raises the cap", roomy, nil, previewTestAccounts, false},
		{"JSON-decoded float raises the cap", nil, map[string]any{"max_result_preview_bytes": float64(previewTestRoomyCapBytes)}, previewTestAccounts, false},
		{"per-call tool_config overrides own", map[string]any{"max_result_preview_bytes": 1}, roomy, previewTestAccounts, false},
		{"zero falls back to the default", nil, map[string]any{"max_result_preview_bytes": float64(0)}, previewTestFitAtDefault, true},
		{"a one-byte cap still returns the first row", nil, map[string]any{"max_result_preview_bytes": 1}, 1, true},
		{"a huge row cap is still bounded by the byte cap", map[string]any{"max_result_preview_rows": 100000}, nil, previewTestFitAtDefault, true},
		{"row cap still applies under a roomy byte cap", map[string]any{"max_result_preview_rows": 3, "max_result_preview_bytes": previewTestRoomyCapBytes}, nil, 3, true},
	}
	for _, tc := range previewToolCases() {
		for _, tt := range tests {
			t.Run(tc.name+"/"+tt.name, func(t *testing.T) {
				toolCtx, facade := newDataToolContext(t)
				ctx := t.Context()
				saveDataToolArtifact(t, ctx, facade, "accounts.json", previewTestAccountsJSON(t), mimeTypeJSON)
				toolCtx.ToolConfig = tt.callConfig

				result, err := tc.newTool(tt.ownConfig).Execute(ctx, tc.args, toolCtx)
				if err != nil {
					t.Fatalf("Execute: %v", err)
				}
				if result.Status != "success" {
					t.Fatalf("status = %q (%s), want success", result.Status, result.Message)
				}
				if got := previewLen(t, result.Data["result_preview"]); got != tt.wantRows {
					t.Errorf("preview rows = %d, want %d", got, tt.wantRows)
				}
				if got := result.Data["result_truncated"]; got != tt.wantTruncated {
					t.Errorf("result_truncated = %v, want %v", got, tt.wantTruncated)
				}
				if got := result.Data["result_rows"]; got != previewTestAccounts {
					t.Errorf("result_rows = %v, want %d", got, previewTestAccounts)
				}
			})
		}
	}
}

func TestTruncatePreviewList(t *testing.T) {
	item := strings.Repeat("x", 98)
	itemBytes := len(`"` + item + `"`)
	items := []string{item, item, item, item}
	tests := []struct {
		name          string
		maxRows       int
		maxBytes      int
		wantLen       int
		wantTruncated bool
	}{
		{"everything fits", 10, 1000, 4, false},
		{"exact fit is not truncated", 10, len("[]") + 4*itemBytes + 3, 4, false},
		{"one byte short drops the last item", 10, len("[]") + 4*itemBytes + 2, 3, true},
		{"keeps the longest fitting prefix rather than halving", 10, len("[]") + 3*itemBytes + 2, 3, true},
		{"row cap applies first", 2, 1000, 2, true},
		{"a lone oversized item is kept but flagged", 10, 10, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated := truncatePreviewList(items, tt.maxRows, tt.maxBytes)
			if len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if encoded, _ := json.Marshal(got); tt.wantLen > 1 && len(encoded) > tt.maxBytes {
				t.Errorf("encoded preview is %d bytes, over the %d cap", len(encoded), tt.maxBytes)
			}
		})
	}
}

func TestTruncatePreviewList_SingleOversizedItem(t *testing.T) {
	got, truncated := truncatePreviewList([]string{strings.Repeat("x", 50)}, 10, 10)
	if len(got) != 1 || !truncated {
		t.Errorf("got (%d items, %v), want (1 item, true)", len(got), truncated)
	}
}

func TestTruncatePreviewList_UnencodableItemEndsThePrefix(t *testing.T) {
	got, truncated := truncatePreviewList([]any{1, math.Inf(1), 3}, 10, 1000)
	if len(got) != 1 || !truncated {
		t.Errorf("got (%v, %v), want ([1], true)", got, truncated)
	}
}

func TestPreviewAccumulator(t *testing.T) {
	row := map[string]any{"v": strings.Repeat("x", 30)}
	rowBytes := len(`{"v":"`) + 30 + len(`"}`) + len(",")
	tests := []struct {
		name     string
		maxRows  int
		maxBytes int
		rows     []map[string]any
		wantKept int
	}{
		{"stops once the byte budget is passed", 1_000_000, len("[]") + 2*rowBytes, []map[string]any{row, row, row, row, row}, 3},
		{"keeps the first row under a tiny budget", 1_000_000, 1, []map[string]any{row, row}, 1},
		{"row cap applies first", 2, 1_000_000, []map[string]any{row, row, row}, 2},
		{"an unencodable row ends retention", 1_000_000, 1_000_000, []map[string]any{row, {"v": math.Inf(1)}, row}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := newPreviewAccumulator(tt.maxRows, tt.maxBytes)
			for _, r := range tt.rows {
				acc.add(r)
			}
			if len(acc.rows) != tt.wantKept {
				t.Errorf("kept %d rows, want %d", len(acc.rows), tt.wantKept)
			}
		})
	}
}

func TestTruncatePreviewList_Empty(t *testing.T) {
	got, truncated := truncatePreviewList([]any{}, 10, 10)
	if len(got) != 0 || truncated {
		t.Errorf("got (%v, %v), want (empty, false)", got, truncated)
	}
}

func TestLayeredIntConfig(t *testing.T) {
	tests := []struct {
		name       string
		callConfig map[string]any
		ownConfig  map[string]any
		want       int
	}{
		{"neither set uses the default", nil, nil, 100},
		{"own config applies", nil, map[string]any{"k": 7}, 7},
		{"per-call config wins over own", map[string]any{"k": float64(9)}, map[string]any{"k": 7}, 9},
		{"invalid per-call value falls through to own", map[string]any{"k": float64(-1)}, map[string]any{"k": 7}, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCtx := &tool.Context{ToolConfig: tt.callConfig}
			if got := layeredIntConfig(toolCtx, tt.ownConfig, "k", 100); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
	if got := layeredIntConfig(nil, map[string]any{"k": 7}, "k", 100); got != 7 {
		t.Errorf("nil tool context: got %d, want 7", got)
	}
}
