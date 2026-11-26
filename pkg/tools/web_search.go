// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

const defaultNumResults = websearch.DefaultNumResults

const WebSearchToolName = websearch.ToolName

const GoogleWebSearchToolName = websearch.GoogleWebSearchToolName

type WebSearchTool struct {
	tool.ReadOnlyTool
	authoredSchemas
	artifactToolBase
	webSearchTurns
	config map[string]any
}

func NewWebSearchTool(config map[string]any) *WebSearchTool {
	if config == nil {
		config = make(map[string]any)
	}
	return &WebSearchTool{config: config}
}

func (t *WebSearchTool) Name() string { return websearch.Name(t.config) }

func (t *WebSearchTool) Description() string { return websearch.Description(t.config) }

func (t *WebSearchTool) Parameters() *tool.JSONSchema { return websearch.Parameters(t.config) }

func (t *WebSearchTool) ResultSchema() *tool.JSONSchema { return webSearchResultSchema() }

func webSearchResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "formatted_results", typ: "string",
			description: "Search results rendered as text for the model to read."},
		resultField{name: "num_results", typ: "integer",
			description: "Count of organic plus image results returned."},
		resultField{name: "search_turn", typ: "integer",
			description: "Which search turn within the task produced these results."},
	)
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	outcome := websearch.Search(ctx, mergeToolConfig(t.config, toolCtx), args, t.hooks(toolCtx))
	return publishWebSearchOutcome(toolCtx, outcome), nil
}

func (w *webSearchTurns) hooks(toolCtx *tool.Context) websearch.Hooks {
	return websearch.Hooks{
		Searching: func(query string) { sendSearchStatus(toolCtx, query) },
		Turn:      func() int { return w.turnFor(toolCtx) },
	}
}

func sendSearchStatus(toolCtx *tool.Context, query string) {
	if toolCtx.SendStatus != nil {
		_ = toolCtx.SendStatus(fmt.Sprintf("Searching the web for: %s", query))
	}
}

func publishWebSearchOutcome(toolCtx *tool.Context, outcome websearch.Outcome) *tool.Result {
	if outcome.Citations != nil && toolCtx.SendSignal != nil {
		_ = toolCtx.SendSignal(*outcome.Citations)
	}
	return outcome.Result
}

func mergeToolConfig(static map[string]any, toolCtx *tool.Context) map[string]any {
	merged := make(map[string]any, len(static)+len(toolCtx.ToolConfig))
	maps.Copy(merged, static)
	maps.Copy(merged, toolCtx.ToolConfig)
	return merged
}

const maxTurnCounterEntries = 10000

type webSearchTurns struct {
	turnCounters sync.Map
}

func (w *webSearchTurns) turnFor(toolCtx *tool.Context) int {
	if toolCtx.SearchTurns != nil {
		return toolCtx.SearchTurns.Next(tool.WebSearchTurnKey)
	}
	return w.nextTurn(toolCtx.ParentTaskID)
}

func (w *webSearchTurns) nextTurn(taskID string) int {
	counter, loaded := w.turnCounters.LoadOrStore(taskID, &atomic.Int64{})
	if !loaded {
		var count int
		w.turnCounters.Range(func(_, _ any) bool {
			count++
			return count < maxTurnCounterEntries
		})
		if count >= maxTurnCounterEntries {
			w.turnCounters.Range(func(key, _ any) bool {
				w.turnCounters.Delete(key)
				return true
			})
		}
	}
	c := counter.(*atomic.Int64)
	return int(c.Add(1) - 1)
}
