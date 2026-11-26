// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveThinkingReplayOnToolResult(t *testing.T) {
	if os.Getenv("SAM_LIVE_LLM_TEST") == "" {
		t.Skip("set SAM_LIVE_LLM_TEST=1 to call the real API")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY is not set")
	}
	model := os.Getenv("SAM_LIVE_MODEL")
	if model == "" {
		model = "claude-opus-4-8"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "anthropic",
		Model:      model,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	tools := []ToolDefinition{{
		Name:        "get_weather",
		Description: "Look up the current weather for a city.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string"},
			},
			"required": []string{"city"},
		},
	}}

	prompt := os.Getenv("SAM_LIVE_PROMPT")
	if prompt == "" {
		prompt = "A pump adds 3.5 L/min to an empty tank while a leak drains 1.25 L/min. " +
			"Work out, step by step, the first whole minute at which the tank exceeds 40 L. " +
			"Then call get_weather with city set to \"Ottawa\" and report both results."
	}
	first := []ChatMessage{{Role: "user", Content: prompt}}

	resp, err := client.ChatCompletion(ctx, &ChatRequest{
		Model:     model,
		Messages:  first,
		Tools:     tools,
		MaxTokens: 4096,
		Reasoning: &ReasoningConfig{BudgetTokens: 2048},
	})
	if err != nil {
		t.Fatalf("turn 1 failed on %s: %v", model, err)
	}
	t.Logf("turn 1: toolCalls=%d reasoningChars=%d reasoningTokens=%d",
		len(resp.ToolCalls), len(resp.ReasoningContent), resp.Usage.ReasoningTokens)
	if len(resp.ToolCalls) == 0 {
		t.Skipf("turn 1 produced no tool call, nothing to replay: %q", resp.Content)
	}
	if resp.Usage.ReasoningTokens == 0 {
		t.Skip("turn 1 produced no thinking, so this run cannot say whether dropping " +
			"thinking blocks is rejected — retry with a prompt the model thinks about")
	}

	second := append([]ChatMessage{}, first...)
	second = append(second, ChatMessage{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})
	for _, call := range resp.ToolCalls {
		second = append(second, ChatMessage{
			Role:       "tool",
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    `{"temperature_c": 21, "conditions": "sunny"}`,
		})
	}

	final, err := client.ChatCompletion(ctx, &ChatRequest{
		Model:     model,
		Messages:  second,
		Tools:     tools,
		MaxTokens: 4096,
		Reasoning: &ReasoningConfig{BudgetTokens: 2048},
	})
	if err != nil {
		if strings.Contains(err.Error(), "thinking") || strings.Contains(err.Error(), "signature") {
			t.Fatalf("the replay constraint bites: %v", err)
		}
		t.Fatalf("turn 2 failed on %s for another reason: %v", model, err)
	}
	t.Logf("turn 2 ok: content=%q reasoningTokens=%d",
		strings.TrimSpace(final.Content), final.Usage.ReasoningTokens)
}
