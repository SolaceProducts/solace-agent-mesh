// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestDescribeToolsTool_AgainstThePlatform(t *testing.T) {
	t.Parallel()
	env := setupControlTest(t)

	res, err := (&tools.DescribeToolsTool{}).Execute(t.Context(), map[string]any{"tools": []any{
		map[string]any{"tool_name": "get_current_time"},
		map[string]any{"tool_name": "no_such_tool_xyz"},
	}}, &tool.Context{ControlQuerier: env.Client})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %s: %s", res.Status, res.Message)
	}
	if want := "Described 2 tools: 1 ok, 1 not_found"; res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}

	preview := res.DataObjects[0].Preview
	if !strings.HasPrefix(preview, "get_current_time: in()") {
		t.Errorf("preview = %q, want get_current_time first with no parameters", preview)
	}
	if !strings.Contains(preview, "toolNode=yes") {
		t.Errorf("preview = %q, want the ok tool's tool-node verdict", preview)
	}
	if !strings.Contains(preview, "no_such_tool_xyz: not_found") {
		t.Errorf("preview = %q, want the unknown tool reported as not_found", preview)
	}
	raw, err := json.Marshal(res.Data["tools"])
	if err != nil {
		t.Fatalf("marshal Data[tools]: %v", err)
	}
	var summaries []map[string]any
	if err := json.Unmarshal(raw, &summaries); err != nil || len(summaries) != 2 {
		t.Fatalf("Data[tools] = %s (err %v), want 2 entries", raw, err)
	}
	if summaries[0]["kind"] != "builtin" {
		t.Errorf("Data[tools][0].kind = %v, want builtin: the platform's kind must reach the Builder", summaries[0]["kind"])
	}
	if !strings.Contains(string(res.DataObjects[0].Content), `"current_time"`) {
		t.Errorf("artifact lacks the declared result fields: %s", res.DataObjects[0].Content)
	}
}
