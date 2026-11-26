// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSearchToolsNumberOnTheTasksOwnCounter(t *testing.T) {
	turns := &tool.SearchTurns{}
	turns.Seed("web_search_google", 3)
	turns.Seed("index_search", 1)
	ctx := &tool.Context{ParentTaskID: "task", SearchTurns: turns}

	if got := (&GoogleWebSearchTool{}).turnFor(ctx); got != 3 {
		t.Fatalf("web search turn = %d, want 3 after three stored searches", got)
	}
	if got := (&IndexSearchTool{}).turnFor(ctx); got != 1 {
		t.Fatalf("index search turn = %d, want 1 after one stored search", got)
	}
	if got := (&GoogleWebSearchTool{}).turnFor(&tool.Context{ParentTaskID: "task"}); got != 0 {
		t.Fatalf("web search turn without a task counter = %d, want 0", got)
	}
}

func TestWebSearchToolsNumberOnOneCounter(t *testing.T) {
	ctx := &tool.Context{ParentTaskID: "task", SearchTurns: &tool.SearchTurns{}}
	google := NewGoogleWebSearchTool(nil)
	connector := NewWebSearchTool(map[string]any{"tool_name": "web_search_ab12cd34"})

	got := []int{google.turnFor(ctx), connector.turnFor(ctx), google.turnFor(ctx)}
	if got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("turns = %v, want [0 1 2] across the two tools", got)
	}
}
