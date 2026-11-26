// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type WebSearchTool struct {
	config map[string]any
	turns  testTurns
}

func NewWebSearchTool(config map[string]any) *WebSearchTool {
	return &WebSearchTool{config: config}
}

func (t *WebSearchTool) Name() string                 { return Name(t.config) }
func (t *WebSearchTool) Description() string          { return Description(t.config) }
func (t *WebSearchTool) Parameters() *tool.JSONSchema { return Parameters(t.config) }

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	return publish(toolCtx, Search(ctx, merged(t.config, toolCtx), args, t.turns.hooks(toolCtx))), nil
}

type GoogleWebSearchTool struct {
	config map[string]any
	turns  testTurns
}

func NewGoogleWebSearchTool(config map[string]any) *GoogleWebSearchTool {
	return &GoogleWebSearchTool{config: config}
}

func (t *GoogleWebSearchTool) Name() string { return GoogleWebSearchToolName }

func (t *GoogleWebSearchTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	return publish(toolCtx, SearchGoogle(ctx, merged(t.config, toolCtx), args, t.turns.hooks(toolCtx))), nil
}

type testTurns struct {
	mu    sync.Mutex
	turns map[string]int
}

func (w *testTurns) hooks(toolCtx *tool.Context) Hooks {
	return Hooks{
		Searching: func(query string) {
			if toolCtx.SendStatus != nil {
				_ = toolCtx.SendStatus(fmt.Sprintf("Searching the web for: %s", query))
			}
		},
		Turn: func() int {
			if toolCtx.SearchTurns != nil {
				return toolCtx.SearchTurns.Next(tool.WebSearchTurnKey)
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			if w.turns == nil {
				w.turns = map[string]int{}
			}
			turn := w.turns[toolCtx.ParentTaskID]
			w.turns[toolCtx.ParentTaskID] = turn + 1
			return turn
		},
	}
}

func publish(toolCtx *tool.Context, outcome Outcome) *tool.Result {
	if outcome.Citations != nil && toolCtx.SendSignal != nil {
		_ = toolCtx.SendSignal(*outcome.Citations)
	}
	return outcome.Result
}

func withConfig(base, extra map[string]any) map[string]any {
	out := maps.Clone(base)
	maps.Copy(out, extra)
	return out
}

func merged(static map[string]any, toolCtx *tool.Context) map[string]any {
	out := make(map[string]any, len(static)+len(toolCtx.ToolConfig))
	maps.Copy(out, static)
	maps.Copy(out, toolCtx.ToolConfig)
	return out
}
