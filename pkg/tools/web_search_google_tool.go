// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

type GoogleWebSearchTool struct {
	tool.ReadOnlyTool
	authoredSchemas
	artifactToolBase
	webSearchTurns
	config map[string]any
}

func NewGoogleWebSearchTool(config map[string]any) *GoogleWebSearchTool {
	if config == nil {
		config = make(map[string]any)
	}
	return &GoogleWebSearchTool{config: config}
}

func (t *GoogleWebSearchTool) Name() string { return GoogleWebSearchToolName }

func (t *GoogleWebSearchTool) Description() string {
	return "Search the web using Google Custom Search API. Use this when you need up-to-date information from Google. " + websearch.CitationGuidance
}

func (t *GoogleWebSearchTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results (1-10)",
				"minimum":     1,
				"maximum":     websearch.GoogleMaxResults,
				"default":     defaultNumResults,
			},
			"search_type": map[string]any{
				"type":        "string",
				"enum":        []string{websearch.SearchTypeImage},
				"description": "Set to 'image' for image search",
			},
			"date_restrict": map[string]any{
				"type":        "string",
				"description": "Restrict results by recency (e.g., 'd7' for last 7 days)",
			},
			"safe_search": map[string]any{
				"type":        "string",
				"enum":        []string{"off", "medium", "high"},
				"description": "Safe search level",
			},
		},
		Required: []string{"query"},
	}
}

func (t *GoogleWebSearchTool) ResultSchema() *tool.JSONSchema { return webSearchResultSchema() }

func (t *GoogleWebSearchTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	outcome := websearch.SearchGoogle(ctx, mergeToolConfig(t.config, toolCtx), args, t.hooks(toolCtx))
	return publishWebSearchOutcome(toolCtx, outcome), nil
}
