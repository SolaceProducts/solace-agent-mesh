// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestExecuteFastFailsWhenInitUnavailable(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{
		ToolName: "dtp_demo_sql_query_019eb14a",
		ToolPassthroughConfig: map[string]any{
			"_str_binary": "execute_sql_query",
		},
	}
	rt := NewTool(cfg, nil)
	rt.markInitUnavailable()

	result, err := rt.Execute(context.Background(), map[string]any{"query": "SELECT 1"}, &tool.Context{AgentName: "agent"})
	if err != nil {
		t.Fatalf("Execute should not return a Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "execute_sql_query") {
		t.Fatalf("error message should name the backing binary, got: %q", result.Message)
	}
	if !strings.Contains(result.Message, "unavailable") {
		t.Fatalf("error message should explain unavailability, got: %q", result.Message)
	}
	wantPrefix := `remote tool "dtp_demo_sql_query_019eb14a": remote tool error [` + string(remotetool.ErrToolNotAvailable) + `]: `
	if !strings.HasPrefix(result.Message, wantPrefix) {
		t.Fatalf("error message should carry the STR rendering with TOOL_NOT_AVAILABLE so evidence classifies it, got: %q", result.Message)
	}
}

func TestInitClearsUnavailable(t *testing.T) {
	t.Parallel()
	cfg := &ToolConfig{ToolName: "mytool"}
	rt := NewTool(cfg, nil)
	rt.markInitUnavailable()
	if !rt.initUnavailable.Load() {
		t.Fatal("precondition: expected initUnavailable set")
	}

	rt.applyInit(&remotetool.InitParams{ToolName: "mytool"}, true)

	if rt.initUnavailable.Load() {
		t.Fatal("applyInit should clear initUnavailable")
	}
}
