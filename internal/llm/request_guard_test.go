// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"log/slog"
	"slices"
	"strings"
	"testing"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

func guardedClient(provider string) *BifrostClient {
	return &BifrostClient{
		cfg:    BifrostConfig{Provider: provider, Model: "claude-sonnet-4-6"},
		logger: slog.New(slog.DiscardHandler),
	}
}

func shape(msgs []schemas.ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		entry := string(m.Role)
		if m.ChatAssistantMessage != nil {
			for _, tc := range m.ToolCalls {
				entry += " " + *tc.ID
			}
		}
		if m.ChatToolMessage != nil {
			entry += " " + *m.ToolCallID
		}
		out[i] = entry
	}
	return out
}

func toolResultText(t *testing.T, m schemas.ChatMessage) string {
	t.Helper()
	if m.Content == nil {
		t.Fatal("tool result has no content")
	}
	if m.Content.ContentStr != nil {
		return *m.Content.ContentStr
	}
	return *m.Content.ContentBlocks[0].Text
}

func call(id string) ToolCall {
	return ToolCall{ID: id, Name: "search", Arguments: `{}`}
}

func TestARequestAnswersAToolCallLeftWithoutAResult(t *testing.T) {
	req := &ChatRequest{Messages: []ChatMessage{
		{Role: "user", Content: "find it"},
		{Role: "assistant", ToolCalls: []ToolCall{call("x")}},
		{Role: "tool", ToolCallID: "x", Content: "found"},
		{Role: "assistant", ToolCalls: []ToolCall{call("y")}},
		{Role: "user", Content: "and then?"},
	}}

	got := mustBuildRequest(t, guardedClient("anthropic"), req).Input

	want := []string{"user", "assistant x", "tool x", "assistant y", "tool y", "user"}
	if !slices.Equal(shape(got), want) {
		t.Fatalf("request = %v, want %v", shape(got), want)
	}
	if text := toolResultText(t, got[4]); text != missingToolResultContent {
		t.Errorf("stand-in result = %q, want %q", text, missingToolResultContent)
	}
}

func TestARequestDropsAToolResultNotRightAfterItsCall(t *testing.T) {
	cases := map[string][]ChatMessage{
		"after a user turn": {
			{Role: "user", Content: "find it"},
			{Role: "assistant", ToolCalls: []ToolCall{call("x")}},
			{Role: "tool", ToolCallID: "x", Content: "found"},
			{Role: "user", Content: "thanks"},
			{Role: "tool", ToolCallID: "x", Content: "found again"},
			{Role: "assistant", Content: "done"},
		},
		"repeating an answered call": {
			{Role: "user", Content: "find it"},
			{Role: "assistant", ToolCalls: []ToolCall{call("x")}},
			{Role: "tool", ToolCallID: "x", Content: "found"},
			{Role: "tool", ToolCallID: "x", Content: "found again"},
			{Role: "user", Content: "thanks"},
			{Role: "assistant", Content: "done"},
		},
		"for a call the turn did not make": {
			{Role: "user", Content: "find it"},
			{Role: "assistant", ToolCalls: []ToolCall{call("x")}},
			{Role: "tool", ToolCallID: "x", Content: "found"},
			{Role: "tool", ToolCallID: "z", Content: "stray"},
			{Role: "user", Content: "thanks"},
			{Role: "assistant", Content: "done"},
		},
	}
	for name, msgs := range cases {
		t.Run(name, func(t *testing.T) {
			got := mustBuildRequest(t, guardedClient("anthropic"), &ChatRequest{Messages: msgs}).Input

			want := []string{"user", "assistant x", "tool x", "user", "assistant"}
			if !slices.Equal(shape(got), want) {
				t.Fatalf("request = %v, want %v", shape(got), want)
			}
			if text := toolResultText(t, got[2]); text != "found" {
				t.Errorf("kept result = %q, want the one right after the call", text)
			}
		})
	}
}

func TestAWellFormedRequestIsSentAsBuilt(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "find both"},
		{Role: "assistant", ToolCalls: []ToolCall{call("x"), call("y")}},
		{Role: "tool", ToolCallID: "y", Content: "found y"},
		{Role: "tool", ToolCallID: "x", Content: "found x"},
		{Role: "assistant", Content: "done"},
	}

	got, dropped, answered := pairToolResults(msgs)

	if dropped != 0 || answered != 0 {
		t.Errorf("dropped %d and answered %d, want neither", dropped, answered)
	}
	if &got[0] != &msgs[0] {
		t.Error("a well-formed request was copied, want it passed through")
	}
}

func dataURL(decodedBytes int) string {
	return "data:image/png;base64," + strings.Repeat("A", (decodedBytes+2)/3*4)
}

func imageResult(url string) []ChatMessage {
	return []ChatMessage{
		{Role: "user", Content: "show me"},
		{Role: "assistant", ToolCalls: []ToolCall{call("x")}},
		{Role: "tool", ToolCallID: "x", ContentBlocks: []ContentBlock{
			{Type: "text", Text: "loaded"},
			{Type: "image_url", ImageURL: url},
		}},
	}
}

func TestAnImageTooLargeForTheProviderIsLeftOut(t *testing.T) {
	msgs := imageResult(dataURL(4_000_000))

	got := mustBuildRequest(t, guardedClient("anthropic"), &ChatRequest{Messages: msgs}).Input

	blocks := got[2].Content.ContentBlocks
	if len(blocks) != 2 || blocks[1].Text == nil || *blocks[1].Text != oversizedImageNote {
		t.Fatalf("tool result blocks = %+v, want the text and a note in place of the image", blocks)
	}
	if msgs[2].ContentBlocks[1].Type != "image_url" {
		t.Error("the caller's message was changed, want only the request changed")
	}
}

func TestAnImageTheProviderTakesIsSent(t *testing.T) {
	cases := map[string]struct {
		provider, model, url string
	}{
		"within the limit":          {"anthropic", "claude-sonnet-4-6", dataURL(3_000_000)},
		"to a provider without one": {"openai", "gpt-5", dataURL(4_000_000)},
		"by address":                {"anthropic", "claude-sonnet-4-6", "https://example.com/big.png"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := guardedClient(tc.provider)
			client.cfg.Model = tc.model

			got := mustBuildRequest(t, client, &ChatRequest{Messages: imageResult(tc.url)}).Input

			blocks := got[2].Content.ContentBlocks
			if len(blocks) != 2 || blocks[1].ImageURLStruct == nil {
				t.Fatalf("tool result blocks = %+v, want the image kept", blocks)
			}
		})
	}
}

func TestBedrockLimitsImagesForEveryModel(t *testing.T) {
	if !limitsInlineImages("bedrock", "amazon.nova-pro-v1:0") {
		t.Error("Bedrock refuses an image over 3.75 MB for every model")
	}
	if !limitsInlineImages("openai", "anthropic/claude-sonnet-4-6") {
		t.Error("a Claude model behind a proxy keeps Anthropic's limit")
	}
}
