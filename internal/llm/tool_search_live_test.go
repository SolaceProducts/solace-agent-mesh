// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveToolSearchDeferredToolRoundTrip(t *testing.T) {
	if os.Getenv("SAM_LIVE_LLM_TEST") == "" {
		t.Skip("set SAM_LIVE_LLM_TEST=1 to call the real API")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	model := os.Getenv("SAM_LIVE_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "anthropic",
		Model:      model,
		BaseURL:    os.Getenv("SAM_LIVE_BASE_URL"),
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	catalog := liveToolCatalog()
	prompt := []ChatMessage{{Role: "user", Content: "What is the status of Jira issue ABC-123? Use your tools."}}

	full := liveStream(ctx, t, client, model, prompt, withDeferral(catalog, false))
	deferred := liveStream(ctx, t, client, model, prompt, withDeferral(catalog, true))
	t.Logf("prompt tokens: all tools loaded %d, deferred %d", full.usage.PromptTokens, deferred.usage.PromptTokens)

	if len(deferred.calls) == 0 {
		t.Fatalf("no tool call with deferred tools; content %q", deferred.content)
	}
	call := deferred.calls[0]
	if call.Name != "get_jira_issue" {
		t.Errorf("called %q, want get_jira_issue", call.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("arguments are not one JSON object (search input merged in?): %q", call.Arguments)
	}
	if _, leaked := args["query"]; leaked {
		t.Errorf("arguments carry the search query: %q", call.Arguments)
	}
	if deferred.usage.PromptTokens >= full.usage.PromptTokens {
		t.Errorf("deferred prompt tokens %d not below full %d", deferred.usage.PromptTokens, full.usage.PromptTokens)
	}

	followUp := append(append([]ChatMessage{}, prompt...),
		ChatMessage{Role: "assistant", ToolCalls: []ToolCall{call}},
		ChatMessage{Role: "tool", ToolCallID: call.ID, Content: `{"key":"ABC-123","status":"In Progress","assignee":"Test User"}`},
	)
	second := liveStream(ctx, t, client, model, followUp, withDeferral(catalog, true))
	if second.content == "" {
		t.Errorf("follow-up turn without replayed search blocks produced no answer (tool calls %v)", second.calls)
	}
	t.Logf("follow-up answer: %d chars, prompt tokens %d", len(second.content), second.usage.PromptTokens)
}

type liveStreamResult struct {
	content string
	calls   []ToolCall
	usage   Usage
}

func liveStream(ctx context.Context, t *testing.T, client *BifrostClient, model string, msgs []ChatMessage, tools []ToolDefinition) liveStreamResult {
	t.Helper()
	stream, err := client.ChatCompletionStream(ctx, &ChatRequest{Model: model, Messages: msgs, Tools: tools, MaxTokens: 1024})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	var res liveStreamResult
	byIndex := map[int]*ToolCall{}
	var order []int
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		res.content += chunk.ContentDelta
		for _, d := range chunk.ToolCallDeltas {
			c, ok := byIndex[d.Index]
			if !ok {
				c = &ToolCall{}
				byIndex[d.Index] = c
				order = append(order, d.Index)
			}
			if d.ID != "" {
				c.ID = d.ID
			}
			if d.Name != "" {
				c.Name = d.Name
			}
			c.Arguments += d.ArgumentsDelta
		}
		if chunk.Usage != nil {
			res.usage = *chunk.Usage
		}
	}
	for _, i := range order {
		res.calls = append(res.calls, *byIndex[i])
	}
	return res
}

func withDeferral(tools []ToolDefinition, deferred bool) []ToolDefinition {
	out := make([]ToolDefinition, len(tools))
	copy(out, tools)
	for i := range out {
		out[i].DeferLoading = deferred
	}
	return out
}

func liveToolCatalog() []ToolDefinition {
	tool := func(name, desc string) ToolDefinition {
		return ToolDefinition{Name: name, Description: desc, Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "string", "description": "Identifier of the record to act on."}},
			"required":   []string{"id"},
		}}
	}
	tools := []ToolDefinition{{
		Name:        "get_jira_issue",
		Description: "Fetch one Jira issue by key, including status, assignee and description.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"issue_key": map[string]any{"type": "string", "description": "Jira issue key, for example ABC-123."}},
			"required":   []string{"issue_key"},
		},
	}}
	for _, area := range []string{"salesforce_account", "salesforce_case", "confluence_page", "datadog_monitor", "datadog_log", "slack_channel", "slack_message", "bamboohr_profile", "postgres_table", "github_pull_request"} {
		for _, verb := range []string{"get", "list", "update"} {
			tools = append(tools, tool(fmt.Sprintf("%s_%s", verb, area),
				fmt.Sprintf("%s a %s record. Returns the full record with metadata, owners, timestamps and related links.", verb, area)+
					strings.Repeat(" It includes every custom field, comment, attachment and change the record carries.", 40)))
		}
	}
	return tools
}
