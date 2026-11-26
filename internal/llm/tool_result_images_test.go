// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"net/http"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

const toolImageURL = "data:image/png;base64,iVBORw0KGgo="

func readImageConversation() []ChatMessage {
	return []ChatMessage{
		{Role: "user", Content: "Describe redpixel.png."},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_read", Name: "load_artifact", Arguments: `{"filename":"redpixel.png"}`},
			{ID: "call_time", Name: "get_time", Arguments: `{}`},
		}},
		{Role: "tool", ToolCallID: "call_read", ToolName: "load_artifact", Content: "redpixel.png (image/png)",
			ContentBlocks: []ContentBlock{{Type: "text", Text: "redpixel.png (image/png)"}, {Type: "image_url", ImageURL: toolImageURL}}},
		{Role: "tool", ToolCallID: "call_time", ToolName: "get_time", Content: `{"time":"12:00"}`},
	}
}

func contentImageURLs(content any) int {
	parts, _ := content.([]any)
	n := 0
	for _, p := range parts {
		if m, ok := p.(map[string]any); ok && m["type"] == "image_url" {
			n++
		}
	}
	return n
}

func TestToolResultImages_OpenAIGatewayMovesThemAfterTheToolResults(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 10, 0) }}
	client := newGatewayClient(t, gw, "gpt-5.6", nil)

	if _, err := client.ChatCompletion(t.Context(), &ChatRequest{Messages: readImageConversation()}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	msgs, _ := gw.body(t, 0)["messages"].([]any)
	var roles []string
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		roles = append(roles, toString(m["role"]))
		if m["role"] == "tool" && contentImageURLs(m["content"]) > 0 {
			t.Errorf("tool message still carries an image: %v", m)
		}
	}
	if want := []string{"user", "assistant", "tool", "tool", "user"}; !slices.Equal(roles, want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if got := contentImageURLs(last["content"]); got != 1 {
		t.Errorf("user message after the tool results carries %d images, want 1: %v", got, last)
	}
}

func TestKeepsToolResultImages(t *testing.T) {
	cases := []struct {
		provider schemas.ModelProvider
		model    string
		want     bool
	}{
		{schemas.Anthropic, "claude-sonnet-4-6", true},
		{schemas.Bedrock, "eu.anthropic.claude-sonnet-4-6", true},
		{schemas.Vertex, "claude-sonnet-4-6", true},
		{schemas.Vertex, "gemini-2.5-pro", false},
		{schemas.Azure, "claude-sonnet-4-6", true},
		{schemas.Azure, "gpt-5.6", false},
		{schemas.Gemini, "gemini-2.5-pro", false},
		{schemas.OpenRouter, "anthropic/claude-sonnet-4-6", false},
		{schemas.OpenAI, "gpt-5.6", false},
	}
	for _, tc := range cases {
		if got := keepsToolResultImages(tc.provider, tc.model); got != tc.want {
			t.Errorf("keepsToolResultImages(%s, %s) = %v, want %v", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestToolResultImages_AnthropicKeepsThemInTheToolResult(t *testing.T) {
	call := captureAnthropicConversation(t, readImageConversation(), nil)

	msgs, _ := call.body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("sent %d messages, want user, assistant and one user turn of tool results: %v", len(msgs), msgs)
	}
	results, _ := msgs[2].(map[string]any)["content"].([]any)
	var images int
	for _, r := range results {
		block, _ := r.(map[string]any)
		inner, _ := block["content"].([]any)
		for _, c := range inner {
			if m, _ := c.(map[string]any); m["type"] == "image" {
				images++
			}
		}
	}
	if images != 1 {
		t.Errorf("tool results carry %d images, want the one the tool returned: %v", images, results)
	}
}

func TestWithToolImagesAsUserMessages_LeavesTheCallersMessagesAlone(t *testing.T) {
	msgs := convertMessages(readImageConversation())

	out := withToolImagesAsUserMessages(msgs)

	if len(out) != len(msgs)+1 {
		t.Fatalf("got %d messages, want one user message added after the tool results", len(out))
	}
	if heading := deref(out[len(out)-1].Content.ContentBlocks[0].Text); heading != "Images returned by the load_artifact tool call above:" {
		t.Errorf("heading = %q, want the tool named", heading)
	}
	if len(msgs[2].Content.ContentBlocks) != 2 {
		t.Errorf("caller's tool message changed: %+v", msgs[2].Content.ContentBlocks)
	}
}
