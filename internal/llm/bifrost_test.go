// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/maximhq/bifrost/core/schemas"
)

func mockOpenAIServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func nonStreamHandler(content, finishReason string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": finishReason,
					"message": map[string]any{
						"role":    "assistant",
						"content": content,
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func toolCallHandler(toolCallID, toolName, toolArgs string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":      "chatcmpl-tools",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]any{
							{
								"id":    toolCallID,
								"type":  "function",
								"index": 0,
								"function": map[string]any{
									"name":      toolName,
									"arguments": toolArgs,
								},
							},
						},
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     15,
				"completion_tokens": 25,
				"total_tokens":      40,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func streamHandler(chunks []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		for i, chunk := range chunks {
			finishReason := ""
			if i == len(chunks)-1 {
				finishReason = "stop"
			}
			data := map[string]any{
				"id":      "chatcmpl-stream",
				"object":  "chat.completion.chunk",
				"created": 1234567890,
				"model":   "test-model",
				"choices": []map[string]any{
					{
						"index":         0,
						"finish_reason": nilOrStr(finishReason),
						"delta": map[string]any{
							"content": chunk,
						},
					},
				},
			}
			jsonBytes, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonBytes)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func nilOrStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newTestClient(t *testing.T, serverURL string) *BifrostClient {
	t.Helper()
	return newTestClientWithModel(t, serverURL, "test-model")
}

func newTestClientWithModel(t *testing.T, serverURL, model string) *BifrostClient {
	t.Helper()

	t.Setenv("OPENAI_API_KEY", "test-key-123")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    model,
		BaseURL:  serverURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Shutdown() })
	return client
}

func TestChatCompletion_NoModel_ErrorsBeforeSend(t *testing.T) {
	var calls atomic.Int32
	server := mockOpenAIServer(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	defer server.Close()

	client := newTestClientWithModel(t, server.URL, "")

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages:      []ChatMessage{{Role: "user", Content: "Hi"}},
		Observability: ObservabilityContext{ComponentName: "gateway-session-title"},
	})
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("error = %v, want ErrNoModel", err)
	}
	if resp != nil {
		t.Errorf("response = %+v, want nil", resp)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider received %d requests, want 0", got)
	}
	if !strings.Contains(err.Error(), "gateway-session-title") {
		t.Errorf("error %q does not name the calling component", err)
	}
	if !strings.Contains(err.Error(), "openai") {
		t.Errorf("error %q does not name the provider", err)
	}
}

func TestChatCompletionStream_NoModel_ErrorsBeforeSend(t *testing.T) {
	var calls atomic.Int32
	server := mockOpenAIServer(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	defer server.Close()

	client := newTestClientWithModel(t, server.URL, "")

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages:      []ChatMessage{{Role: "user", Content: "Hi"}},
		Observability: ObservabilityContext{ComponentName: "agent-test"},
	})
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("error = %v, want ErrNoModel", err)
	}
	if stream != nil {
		t.Error("stream is non-nil, want nil")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider received %d requests, want 0", got)
	}
}

func TestChatCompletionNonStream(t *testing.T) {
	server := mockOpenAIServer(t, nonStreamHandler("Hello, world!", "stop"))
	defer server.Close()

	client := newTestClient(t, server.URL)

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Hello, world!" {
		t.Errorf("content = %q, want %q", resp.Content, "Hello, world!")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage == nil {
		t.Fatal("expected usage")
	}
	if resp.Usage.TotalTokens != 30 {
		t.Errorf("total_tokens = %d, want 30", resp.Usage.TotalTokens)
	}
}

func TestChatCompletion_KeylessCustomEndpoint(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	var mu sync.Mutex
	var gotAuth string
	server := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		nonStreamHandler("keyless ok", "stop")(w, r)
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  server.URL,
	})
	if err != nil {
		t.Fatalf("client init failed for keyless custom endpoint: %v", err)
	}
	defer client.Shutdown()

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("chat completion failed against keyless endpoint: %v", err)
	}
	if resp.Content != "keyless ok" {
		t.Errorf("content = %q, want %q", resp.Content, "keyless ok")
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(gotAuth, keylessAPIKeyPlaceholder) {
		t.Errorf("Authorization header = %q, want it to contain %q", gotAuth, keylessAPIKeyPlaceholder)
	}
}

func TestChatCompletionStream(t *testing.T) {
	chunks := []string{"Hello", ", ", "world", "!"}
	server := mockOpenAIServer(t, streamHandler(chunks))
	defer server.Close()

	client := newTestClient(t, server.URL)

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	var lastChunk StreamChunk
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		content.WriteString(chunk.ContentDelta)
		lastChunk = chunk
	}

	if content.String() != "Hello, world!" {
		t.Errorf("content = %q, want %q", content.String(), "Hello, world!")
	}
	if lastChunk.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", lastChunk.FinishReason, "stop")
	}
}

func TestChatCompletionToolCalls(t *testing.T) {
	server := mockOpenAIServer(t, toolCallHandler(
		"call-123",
		"calculator",
		`{"operation":"multiply","a":6,"b":7}`,
	))
	defer server.Close()

	client := newTestClient(t, server.URL)

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 6 * 7?"},
		},
		Tools: []ToolDefinition{
			{
				Name:        "calculator",
				Description: "Perform arithmetic",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"operation": map[string]any{"type": "string"},
						"a":         map[string]any{"type": "number"},
						"b":         map[string]any{"type": "number"},
					},
					"required": []any{"operation", "a", "b"},
				},
			},
		},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want %q", resp.FinishReason, "tool_calls")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call-123" {
		t.Errorf("tool call ID = %q, want %q", tc.ID, "call-123")
	}
	if tc.Name != "calculator" {
		t.Errorf("tool call name = %q, want %q", tc.Name, "calculator")
	}
	if tc.Arguments != `{"operation":"multiply","a":6,"b":7}` {
		t.Errorf("tool call args = %q", tc.Arguments)
	}
}

func TestMessageConversion(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!", ToolCalls: []ToolCall{
			{ID: "tc-1", Name: "search", Arguments: `{"q":"test"}`},
		}},
		{Role: "tool", Content: "result", ToolCallID: "tc-1"},
	}

	converted := convertMessages(msgs)
	if len(converted) != 4 {
		t.Fatalf("got %d messages, want 4", len(converted))
	}

	if string(converted[0].Role) != "system" {
		t.Errorf("msg[0].Role = %q", converted[0].Role)
	}

	if converted[2].ChatAssistantMessage == nil {
		t.Fatal("expected assistant message with tool calls")
	}
	if len(converted[2].ToolCalls) != 1 {
		t.Errorf("tool calls = %d", len(converted[2].ToolCalls))
	}

	if converted[3].ChatToolMessage == nil {
		t.Fatal("expected tool message")
	}
	if *converted[3].ToolCallID != "tc-1" {
		t.Errorf("tool call ID = %q", *converted[3].ToolCallID)
	}
}

var validAnthropicToolUseID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func TestSanitizeToolCallID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean openai", "call_06a044db2a024eb79650f7017c9d", "call_06a044db2a024eb79650f7017c9d"},
		{"simple", "tc-1", "tc-1"},
		{"gemini thought signature", "call_67fb__thought__AY89a1/PbAJVO3KKlnHw4ThC/n+0yuCoMXH", "call_67fb__thought__AY89a1_PbAJVO3KKlnHw4ThC_n_0yuCoMXH"},
		{"empty", "", ""},
		{"only invalid", "/+=", "___"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeToolCallID(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeToolCallID(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got != "" && !validAnthropicToolUseID.MatchString(got) {
				t.Errorf("result %q does not match Anthropic tool_use.id pattern", got)
			}
		})
	}
}

func TestMessageConversionSanitizesGeminiToolCallID(t *testing.T) {
	poison := "call_67fb75399fcc4f23a1954c53dec6__thought__AY89a1/PbAJVO3KKlnHw4ThC/n+0yuCoMXH"
	msgs := []ChatMessage{
		{Role: "user", Content: "ping"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: poison, Name: "search", Arguments: `{}`}}},
		{Role: "tool", Content: "result", ToolCallID: poison},
	}

	converted := convertMessages(msgs)

	assistantID := *converted[1].ToolCalls[0].ID
	toolResultID := *converted[2].ToolCallID
	if !validAnthropicToolUseID.MatchString(assistantID) {
		t.Errorf("assistant tool_use id %q not Anthropic-valid", assistantID)
	}
	if !validAnthropicToolUseID.MatchString(toolResultID) {
		t.Errorf("tool_result id %q not Anthropic-valid", toolResultID)
	}
	if assistantID != toolResultID {
		t.Errorf("assistant id %q and tool_result id %q diverged; pair would break", assistantID, toolResultID)
	}
}

func TestErrorFormat(t *testing.T) {
	err := &Error{Message: "rate limited", StatusCode: 429}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %q", err.Error())
	}

	err2 := &Error{Message: "unknown"}
	if strings.Contains(err2.Error(), "status") {
		t.Errorf("error with 0 status should not show status: %q", err2.Error())
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name             string
		err              Error
		wantBadRequest   bool
		wantContextLimit bool
		wantMsgContains  string
	}{
		{
			name:             "context limit - too many tokens",
			err:              Error{Message: "This model's maximum context length is exceeded. Too many tokens in the request.", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "context limit - input is too long",
			err:              Error{Message: "Input is too long for the requested model.", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "start a new conversation",
		},
		{
			name:             "context limit - too many input tokens",
			err:              Error{Message: "Request has too many input tokens", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "context limit - too many tokens without rate wording",
			err:              Error{Message: "too many tokens: total number of tokens in the prompt cannot exceed 4081 - received 5000", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "context limit - explicit code outranks rate wording",
			err:              Error{Message: "Rate limit reached: too many tokens per minute", StatusCode: 400, Code: "context_length_exceeded"},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "context limit - input token limit exceeded",
			err:              Error{Message: "input token limit exceeded", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "context limit - prompt too long with usage limit wording",
			err:              Error{Message: "prompt is too long: usage limit exceeded for context", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "throttle 400 - too many tokens, please wait",
			err:              Error{Message: "Too many tokens, please wait before trying again.", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "Bad request: Too many tokens, please wait",
		},
		{
			name:             "throttle 400 - too many tokens per minute",
			err:              Error{Message: "Too many tokens per minute", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "Bad request: Too many tokens per minute",
		},
		{
			name:             "throttle 400 - token limit per minute exceeded",
			err:              Error{Message: "token limit per minute exceeded", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "Bad request: token limit per minute exceeded",
		},
		{
			name:             "throttle 400 - rate limit reached on tokens per min",
			err:              Error{Message: "Rate limit reached for gpt-4o in organization org-abc123 on tokens per min (TPM): Limit 30000, Used 29000, Requested 2000. Please try again in 2s.", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "Bad request: Rate limit reached",
		},
		{
			name:             "throttle 429 - per-minute token limit",
			err:              Error{Message: "Number of request tokens has exceeded your per-minute token limit.", StatusCode: 429},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  RateLimitedMessagePrefix + "Number of request tokens",
		},
		{
			name:             "throttle 429 - too many tokens without rate wording",
			err:              Error{Message: "Too many tokens", StatusCode: 429},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  RateLimitedMessagePrefix + "Too many tokens",
		},
		{
			name:             "throttle 429 - token limit with a retry delay",
			err:              Error{Message: "Token limit is exceeded. Try again in 6 seconds.", StatusCode: 429},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  RateLimitedMessagePrefix + "Token limit is exceeded",
		},
		{
			name:             "context limit - explicit code on a 429",
			err:              Error{Message: "Too many tokens", StatusCode: 429, Code: "context_length_exceeded"},
			wantBadRequest:   true,
			wantContextLimit: true,
			wantMsgContains:  "conversation history has become too long",
		},
		{
			name:             "bad request - content policy",
			err:              Error{Message: "Content policy violation detected", StatusCode: 400},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "Bad request: Content policy violation",
		},
		{
			name:             "rate limit 429",
			err:              Error{Message: "Rate limit exceeded", StatusCode: 429},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  RateLimitedMessagePrefix + "Rate limit exceeded",
		},
		{
			name:             "rate limit wording without a status",
			err:              Error{Message: "Rate limit exceeded"},
			wantBadRequest:   false,
			wantContextLimit: false,
			wantMsgContains:  RateLimitedMessagePrefix + "Rate limit exceeded",
		},
		{
			name:             "429 over quota keeps the provider's words",
			err:              Error{Message: "You exceeded your current quota", StatusCode: 429, Code: "insufficient_quota"},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "Bad request: You exceeded your current quota",
		},
		{
			name:             "429 with an HTML page renders no markup",
			err:              Error{Message: "<html><body>Too Many Requests</body></html>", StatusCode: 429},
			wantBadRequest:   true,
			wantContextLimit: false,
			wantMsgContains:  "The AI provider rejected this request (HTTP 429)",
		},
		{
			name:             "server error 500",
			err:              Error{Message: "Internal server error", StatusCode: 500},
			wantBadRequest:   false,
			wantContextLimit: false,
			wantMsgContains:  "LLM error: Internal server error",
		},
		{
			name:             "no status code",
			err:              Error{Message: "connection timeout"},
			wantBadRequest:   false,
			wantContextLimit: false,
			wantMsgContains:  "LLM error: connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsBadRequest(); got != tt.wantBadRequest {
				t.Errorf("IsBadRequest() = %v, want %v", got, tt.wantBadRequest)
			}
			if got := tt.err.IsContextLimitError(); got != tt.wantContextLimit {
				t.Errorf("IsContextLimitError() = %v, want %v", got, tt.wantContextLimit)
			}
			msg := tt.err.UserFacingMessage()
			if !strings.Contains(msg, tt.wantMsgContains) {
				t.Errorf("UserFacingMessage() = %q, want it to contain %q", msg, tt.wantMsgContains)
			}
		})
	}
}

func TestDefaultAPIKeyEnv(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"openai", "OPENAI_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"custom", "CUSTOM_API_KEY"},
	}
	for _, tt := range tests {
		got := defaultAPIKeyEnv(tt.provider)
		if got != tt.want {
			t.Errorf("defaultAPIKeyEnv(%q) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestMessageConversionWithCacheControl(t *testing.T) {
	plain := convertMessage(ChatMessage{Role: "system", Content: "Hello"})
	if plain.Content == nil || plain.Content.ContentStr == nil {
		t.Fatal("plain message should use ContentStr")
	}
	if len(plain.Content.ContentBlocks) != 0 {
		t.Error("plain message should not have ContentBlocks")
	}

	cached := convertMessage(ChatMessage{
		Role:         "system",
		Content:      "You are helpful.",
		CacheControl: &CacheControl{Type: "ephemeral"},
	})
	if cached.Content == nil {
		t.Fatal("cached message should have Content")
	}
	if cached.Content.ContentStr != nil {
		t.Error("cached message should not use ContentStr")
	}
	if len(cached.Content.ContentBlocks) != 1 {
		t.Fatalf("cached message should have 1 ContentBlock, got %d", len(cached.Content.ContentBlocks))
	}
	block := cached.Content.ContentBlocks[0]
	if block.Text == nil || *block.Text != "You are helpful." {
		t.Errorf("block text = %v, want %q", block.Text, "You are helpful.")
	}
	if block.CacheControl == nil {
		t.Fatal("block should have CacheControl")
	}
	if string(block.CacheControl.Type) != "ephemeral" {
		t.Errorf("block CacheControl.Type = %q, want %q", block.CacheControl.Type, "ephemeral")
	}

	cached1h := convertMessage(ChatMessage{
		Role:         "system",
		Content:      "test",
		CacheControl: &CacheControl{Type: "ephemeral", TTL: "1h"},
	})
	block1h := cached1h.Content.ContentBlocks[0]
	if block1h.CacheControl.TTL == nil || *block1h.CacheControl.TTL != "1h" {
		t.Error("expected TTL=1h on cache control")
	}
}

func TestMultiBlockMessageCacheControl(t *testing.T) {
	cached := convertMessage(ChatMessage{
		Role:       "tool",
		ToolCallID: "call-1",
		ContentBlocks: []ContentBlock{
			{Type: "text", Text: "tool output line 1"},
			{Type: "text", Text: "tool output line 2"},
		},
		CacheControl: &CacheControl{Type: "ephemeral"},
	})

	blocks := cached.Content.ContentBlocks
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(blocks))
	}
	if blocks[0].CacheControl != nil {
		t.Error("first block should not have CacheControl")
	}
	if blocks[1].CacheControl == nil {
		t.Fatal("last block should have CacheControl set")
	}
	if string(blocks[1].CacheControl.Type) != "ephemeral" {
		t.Errorf("last block CacheControl.Type = %q, want ephemeral", blocks[1].CacheControl.Type)
	}
}

func TestMultiBlockMessageNoCacheControl(t *testing.T) {
	conv := convertMessage(ChatMessage{
		Role:          "tool",
		ToolCallID:    "call-1",
		ContentBlocks: []ContentBlock{{Type: "text", Text: "out"}},
	})
	if conv.Content.ContentBlocks[0].CacheControl != nil {
		t.Error("block should not have CacheControl when none requested")
	}
}

func TestConvertMessageFileBlock(t *testing.T) {
	conv := convertMessage(ChatMessage{
		Role: "user",
		ContentBlocks: []ContentBlock{
			{Type: "text", Text: "read this"},
			{
				Type:     "file",
				FileData: "data:application/pdf;base64,JVBERi0x",
				FileName: "scan.pdf",
				FileType: "application/pdf",
			},
		},
	})
	blocks := conv.Content.ContentBlocks
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(blocks))
	}
	fb := blocks[1]
	if fb.Type != schemas.ChatContentBlockTypeFile {
		t.Fatalf("block type = %q, want %q", fb.Type, schemas.ChatContentBlockTypeFile)
	}
	if fb.File == nil {
		t.Fatal("file block has nil File")
	}
	if fb.File.FileData == nil || *fb.File.FileData != "data:application/pdf;base64,JVBERi0x" {
		t.Errorf("FileData = %v, want the PDF data URI", fb.File.FileData)
	}
	if fb.File.Filename == nil || *fb.File.Filename != "scan.pdf" {
		t.Errorf("Filename = %v, want scan.pdf", fb.File.Filename)
	}
	if fb.File.FileType == nil || *fb.File.FileType != "application/pdf" {
		t.Errorf("FileType = %v, want application/pdf", fb.File.FileType)
	}
}

func TestMultiBlockMessageUnknownBlockType(t *testing.T) {
	conv := convertMessage(ChatMessage{
		Role:          "user",
		ContentBlocks: []ContentBlock{{Type: "video_url", Text: "ignored"}},
		CacheControl:  &CacheControl{Type: "ephemeral"},
	})
	if conv.Content != nil && len(conv.Content.ContentBlocks) != 0 {
		t.Errorf("unknown block type should emit no content blocks, got %d", len(conv.Content.ContentBlocks))
	}
}

func TestConvertMessage_BackfillsEmptyUserSystemContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  ChatMessage
	}{
		{"empty user text", ChatMessage{Role: "user", Content: ""}},
		{"whitespace user text", ChatMessage{Role: "user", Content: "   "}},
		{"user unknown-only blocks", ChatMessage{Role: "user", ContentBlocks: []ContentBlock{{Type: "video_url", Text: "x"}}}},
		{"user empty-text block only", ChatMessage{Role: "user", ContentBlocks: []ContentBlock{{Type: "text", Text: ""}}}},
		{"user whitespace-text block only", ChatMessage{Role: "user", ContentBlocks: []ContentBlock{{Type: "text", Text: "   "}}}},
		{"empty system text", ChatMessage{Role: "system", Content: ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cm := convertMessage(tc.msg)
			if !hasContent(cm.Content) {
				t.Fatalf("empty %s message must be backfilled with non-empty content", tc.msg.Role)
			}
			if cm.Content.ContentStr == nil || strings.TrimSpace(*cm.Content.ContentStr) == "" {
				t.Errorf("backfill must be non-whitespace (Bedrock rejects whitespace-only), got %+v", cm.Content)
			}
		})
	}

	t.Run("assistant tool-call turn not backfilled", func(t *testing.T) {
		cm := convertMessage(ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "tc1", Name: "fn", Arguments: "{}"}}})
		if hasContent(cm.Content) {
			t.Errorf("assistant tool-call turn should keep empty content, got %+v", cm.Content)
		}
	})
	t.Run("tool-result message not backfilled", func(t *testing.T) {
		cm := convertMessage(ChatMessage{Role: "tool", ToolCallID: "tc1"})
		if hasContent(cm.Content) {
			t.Errorf("tool message should keep empty content, got %+v", cm.Content)
		}
	})

	t.Run("whitespace text block dropped when other content present", func(t *testing.T) {
		cm := convertMessage(ChatMessage{Role: "user", ContentBlocks: []ContentBlock{
			{Type: "text", Text: "  "},
			{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"},
		}})
		for _, b := range cm.Content.ContentBlocks {
			if string(b.Type) == "text" {
				t.Errorf("whitespace-only text block must be dropped, got %+v", b)
			}
		}
		if !hasContent(cm.Content) {
			t.Error("the image block must remain")
		}
	})
}

func TestToolConversionWithCacheControl(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "alpha", Description: "first"},
		{
			Name:         "beta",
			Description:  "second",
			CacheControl: &CacheControl{Type: "ephemeral", TTL: "1h"},
		},
	}

	converted := convertTools(tools)

	if converted[0].CacheControl != nil {
		t.Error("first tool should not have CacheControl")
	}

	if converted[1].CacheControl == nil {
		t.Fatal("second tool should have CacheControl")
	}
	if string(converted[1].CacheControl.Type) != "ephemeral" {
		t.Errorf("tool CacheControl.Type = %q, want %q", converted[1].CacheControl.Type, "ephemeral")
	}
	if converted[1].CacheControl.TTL == nil || *converted[1].CacheControl.TTL != "1h" {
		t.Error("expected TTL=1h on tool cache control")
	}
}

func TestCachedTokensInNonStreamResponse(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message":       map[string]any{"role": "assistant", "content": "cached reply"},
			}},
			"usage": map[string]any{
				"prompt_tokens":     100,
				"completion_tokens": 20,
				"total_tokens":      120,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 80,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newTestClient(t, server.URL)
	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage")
	}
	if resp.Usage.CachedTokens != 80 {
		t.Errorf("CachedTokens = %d, want 80", resp.Usage.CachedTokens)
	}
}

func TestToBifrostCacheControlNil(t *testing.T) {
	if got := toBifrostCacheControl(nil); got != nil {
		t.Errorf("toBifrostCacheControl(nil) = %v, want nil", got)
	}
}

func TestExtractCachedTokensNilUsage(t *testing.T) {
	if got := extractCachedTokens(nil); got != 0 {
		t.Errorf("extractCachedTokens(nil) = %d, want 0", got)
	}
}

func TestExtractCacheWriteTokensNilUsage(t *testing.T) {
	if got := extractCacheWriteTokens(nil); got != 0 {
		t.Errorf("extractCacheWriteTokens(nil) = %d, want 0", got)
	}
}

func TestCacheWriteTokensInNonStreamResponse(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1234567890,
			"model": "test-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "reply"},
			}},
			"usage": map[string]any{
				"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120,
				"prompt_tokens_details": map[string]any{
					"cached_read_tokens":  80,
					"cached_write_tokens": 30,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newTestClient(t, server.URL)
	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage")
	}
	if resp.Usage.CachedTokens != 110 {
		t.Errorf("CachedTokens = %d, want 110 (80 read + 30 write)", resp.Usage.CachedTokens)
	}
	if resp.Usage.CacheWriteTokens != 30 {
		t.Errorf("CacheWriteTokens = %d, want 30", resp.Usage.CacheWriteTokens)
	}
}

func TestExtractCacheTokensAnthropicShapeDoesNotDoubleCountCachedInput(t *testing.T) {
	usage := &schemas.BifrostLLMUsage{
		PromptTokens: 1000,
		PromptTokensDetails: &schemas.ChatPromptTokensDetails{
			CachedReadTokens:  800,
			CachedWriteTokens: 150,
		},
	}
	if got := extractCachedTokens(usage); got != 950 {
		t.Errorf("extractCachedTokens = %d, want 950 (800 read + 150 write)", got)
	}
	if got := extractCacheWriteTokens(usage); got != 150 {
		t.Errorf("extractCacheWriteTokens = %d, want 150", got)
	}
	if nonCached := usage.PromptTokens - extractCachedTokens(usage); nonCached != 50 {
		t.Errorf("non-cached input = %d, want 50", nonCached)
	}
}

func TestConvertMessageWithContentBlocks(t *testing.T) {
	msg := ChatMessage{
		Role:       "tool",
		Content:    `{"status":"success"}`,
		ToolCallID: "tc-img",
		ContentBlocks: []ContentBlock{
			{Type: "text", Text: `{"status":"success","content":"Image loaded."}`},
			{Type: "image_url", ImageURL: "data:image/png;base64,iVBORw0KGgo="},
		},
	}

	cm := convertMessage(msg)

	if cm.Content == nil {
		t.Fatal("expected non-nil content")
	}
	if cm.Content.ContentStr != nil {
		t.Error("expected ContentStr to be nil when ContentBlocks are set")
	}
	if len(cm.Content.ContentBlocks) != 2 {
		t.Fatalf("got %d content blocks, want 2", len(cm.Content.ContentBlocks))
	}

	if string(cm.Content.ContentBlocks[0].Type) != "text" {
		t.Errorf("block[0].Type = %q, want text", cm.Content.ContentBlocks[0].Type)
	}
	if cm.Content.ContentBlocks[0].Text == nil || *cm.Content.ContentBlocks[0].Text == "" {
		t.Error("block[0].Text should be non-empty")
	}

	if string(cm.Content.ContentBlocks[1].Type) != "image_url" {
		t.Errorf("block[1].Type = %q, want image_url", cm.Content.ContentBlocks[1].Type)
	}
	if cm.Content.ContentBlocks[1].ImageURLStruct == nil {
		t.Fatal("block[1].ImageURLStruct should be non-nil")
	}
	if !strings.HasPrefix(cm.Content.ContentBlocks[1].ImageURLStruct.URL, "data:image/png;base64,") {
		t.Errorf("block[1] URL = %q", cm.Content.ContentBlocks[1].ImageURLStruct.URL)
	}

	if cm.ChatToolMessage == nil || *cm.ToolCallID != "tc-img" {
		t.Error("expected ChatToolMessage with ToolCallID")
	}
}

func TestConvertMessageWithAudioContentBlock(t *testing.T) {
	msg := ChatMessage{
		Role: "user",
		ContentBlocks: []ContentBlock{
			{Type: "text", Text: "what sound is this?"},
			{Type: "input_audio", AudioData: "YUJD", AudioFormat: "mp3"},
		},
	}

	cm := convertMessage(msg)

	if cm.Content == nil || len(cm.Content.ContentBlocks) != 2 {
		t.Fatalf("unexpected content blocks: %+v", cm.Content)
	}
	audio := cm.Content.ContentBlocks[1]
	if string(audio.Type) != "input_audio" {
		t.Errorf("block[1].Type = %q, want input_audio", audio.Type)
	}
	if audio.InputAudio == nil {
		t.Fatal("block[1].InputAudio should be non-nil")
	}
	if audio.InputAudio.Data != "YUJD" {
		t.Errorf("audio.Data = %q, want YUJD", audio.InputAudio.Data)
	}
	if audio.InputAudio.Format == nil || *audio.InputAudio.Format != "mp3" {
		t.Errorf("audio.Format = %v, want mp3", audio.InputAudio.Format)
	}
}

func TestConvertMessageWithoutContentBlocks(t *testing.T) {
	msg := ChatMessage{
		Role:    "user",
		Content: "hello",
	}
	cm := convertMessage(msg)
	if cm.Content == nil || cm.Content.ContentStr == nil {
		t.Fatal("expected ContentStr")
	}
	if *cm.Content.ContentStr != "hello" {
		t.Errorf("ContentStr = %q", *cm.Content.ContentStr)
	}
}

func TestShutdownUnsetsOwnedEnvVars(t *testing.T) {
	server := mockOpenAIServer(t, nonStreamHandler("ok", "stop"))
	defer server.Close()

	envName := "SAM_AWE_LLM_API_KEY_TESTSHUTDOWN"
	t.Setenv(envName, "sk-test")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:     "openai",
		Model:        "test-model",
		BaseURL:      server.URL,
		APIKeyEnvVar: envName,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := os.LookupEnv(envName); !ok {
		t.Fatalf("env var %s should still be set before Shutdown", envName)
	}

	client.Shutdown()

	if v, ok := os.LookupEnv(envName); ok {
		t.Errorf("env var %s should be unset after Shutdown, got %q", envName, v)
	}
}

func TestNewBifrostClient_OAuthEnvUnsetOnInitFailure(t *testing.T) {
	envBefore := captureSAMEnv()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "no-such-provider-xyzzy",
		Model:    "test-model",
		BaseURL:  "http://127.0.0.1:1",
		OAuth2: &oauth2cc.Config{
			TokenURL:     "http://127.0.0.1:1/token",
			ClientID:     "test",
			ClientSecret: "test",
		},
	})
	if err == nil {
		t.Fatal("expected NewBifrostClient to fail with an unknown provider")
	}

	envAfter := captureSAMEnv()
	for name := range envAfter {
		if !envBefore[name] {
			t.Errorf("env var %q leaked from failed NewBifrostClient", name)
		}
	}
}

func captureSAMEnv() map[string]bool {
	out := make(map[string]bool)
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		name := kv[:eq]
		if strings.HasPrefix(name, "SAM_AWE_LLM_API_KEY_") || strings.HasPrefix(name, "SAM_OAUTH2_TOKEN_") {
			out[name] = true
		}
	}
	return out
}

func TestShutdownLeavesUnownedEnvVarsAlone(t *testing.T) {
	server := mockOpenAIServer(t, nonStreamHandler("ok", "stop"))
	defer server.Close()

	t.Setenv("OPENAI_API_KEY", "sk-shared")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:     "openai",
		Model:        "test-model",
		BaseURL:      server.URL,
		APIKeyEnvVar: "OPENAI_API_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}

	client.Shutdown()

	if _, ok := os.LookupEnv("OPENAI_API_KEY"); !ok {
		t.Errorf("OPENAI_API_KEY (no SAM_ prefix) must not be unset by Shutdown")
	}
}

func TestBifrostErrorToError(t *testing.T) {
	tests := []struct {
		name       string
		bfErr      *schemas.BifrostError
		wantNil    bool
		wantMsg    string
		wantStatus int
	}{
		{
			name:    "nil error returns nil",
			bfErr:   nil,
			wantNil: true,
		},
		{
			name: "error with message and status code",
			bfErr: &schemas.BifrostError{
				Error:      &schemas.ErrorField{Message: "rate limited"},
				StatusCode: intPtr(429),
			},
			wantMsg:    "rate limited",
			wantStatus: 429,
		},
		{
			name: "error with nil inner Error",
			bfErr: &schemas.BifrostError{
				StatusCode: intPtr(500),
			},
			wantMsg:    "bifrost error",
			wantStatus: 500,
		},
		{
			name: "error with nil StatusCode",
			bfErr: &schemas.BifrostError{
				Error: &schemas.ErrorField{Message: "timeout"},
			},
			wantMsg:    "timeout",
			wantStatus: 0,
		},
		{
			name:       "error with all nil fields",
			bfErr:      &schemas.BifrostError{},
			wantMsg:    "bifrost error",
			wantStatus: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmErr := bifrostErrorToError(tt.bfErr, "")
			if tt.wantNil {
				if llmErr != nil {
					t.Fatalf("expected nil, got %v", llmErr)
				}
				return
			}
			if llmErr == nil {
				t.Fatal("expected non-nil error")
			}
			if llmErr.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", llmErr.Message, tt.wantMsg)
			}
			if llmErr.StatusCode != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", llmErr.StatusCode, tt.wantStatus)
			}
		})
	}
}

func intPtr(i int) *int { return &i }

func TestExtractContentStrWithContentBlocks(t *testing.T) {
	s := "hello"
	c := &schemas.ChatMessageContent{ContentStr: &s}
	if got := extractContentStr(c); got != "hello" {
		t.Errorf("with ContentStr: got %q, want %q", got, "hello")
	}

	text1 := "part one"
	text2 := " part two"
	c2 := &schemas.ChatMessageContent{
		ContentBlocks: []schemas.ChatContentBlock{
			{Text: &text1},
			{Text: &text2},
			{Text: nil},
		},
	}
	if got := extractContentStr(c2); got != "part one part two" {
		t.Errorf("with ContentBlocks: got %q, want %q", got, "part one part two")
	}

	if got := extractContentStr(nil); got != "" {
		t.Errorf("nil: got %q, want empty", got)
	}

	if got := extractContentStr(&schemas.ChatMessageContent{}); got != "" {
		t.Errorf("empty: got %q, want empty", got)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{0, false},
	}
	for _, tt := range tests {
		err := &Error{Message: "test", StatusCode: tt.status}
		if got := err.IsRetryable(); got != tt.want {
			t.Errorf("IsRetryable() with status %d = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestChatCompletionForwardsToolSchemaConstraints(t *testing.T) {
	var body []byte
	respond := nonStreamHandler("ok", "stop")
	server := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		respond(w, r)
	})
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools: []ToolDefinition{
			{
				Name: "go_built",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"turn": map[string]any{"type": "integer"},
					},
					"required":             []string{"turn"},
					"additionalProperties": false,
				},
			},
			{
				Name: "json_decoded",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
					"required":             []any{"query"},
					"additionalProperties": map[string]any{"type": "string"},
				},
			},
			{
				Name: "unconstrained",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"note": map[string]any{"type": "string"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var sent struct {
		Tools []struct {
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	params := map[string]map[string]any{}
	for _, tool := range sent.Tools {
		params[tool.Function.Name] = tool.Function.Parameters
	}

	if got := fmt.Sprint(params["go_built"]["required"]); got != "[turn]" {
		t.Errorf("go_built required = %s, want [turn]", got)
	}
	if got, ok := params["go_built"]["additionalProperties"].(bool); !ok || got {
		t.Errorf("go_built additionalProperties = %v, want false", params["go_built"]["additionalProperties"])
	}
	if got := fmt.Sprint(params["json_decoded"]["required"]); got != "[query]" {
		t.Errorf("json_decoded required = %s, want [query]", got)
	}
	if got := fmt.Sprint(params["json_decoded"]["additionalProperties"]); got != "map[type:string]" {
		t.Errorf("json_decoded additionalProperties = %s, want map[type:string]", got)
	}
	if _, present := params["unconstrained"]["required"]; present {
		t.Errorf("unconstrained must omit required, got %v", params["unconstrained"]["required"])
	}
	if _, present := params["unconstrained"]["additionalProperties"]; present {
		t.Errorf("unconstrained must omit additionalProperties, got %v", params["unconstrained"]["additionalProperties"])
	}
}

func TestChatCompletionResolvesToolSchemaDefinitionRefs(t *testing.T) {
	itemDef := map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []any{"name"},
	}
	pydantic := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Item"}},
			"owner": map[string]any{"$ref": "#/$defs/Item", "description": "who owns the order"},
		},
		"required": []any{"items"},
		"$defs":    map[string]any{"Item": itemDef},
	}
	legacy := map[string]any{
		"type":        "object",
		"properties":  map[string]any{"item": map[string]any{"$ref": "#/definitions/Item"}},
		"definitions": map[string]any{"Item": itemDef},
	}
	cyclic := map[string]any{
		"type":       "object",
		"properties": map[string]any{"tree": map[string]any{"$ref": "#/$defs/Node"}},
		"$defs": map[string]any{
			"Node": map[string]any{
				"type":       "object",
				"properties": map[string]any{"child": map[string]any{"$ref": "#/$defs/Node"}},
			},
		},
	}
	plainProperties := map[string]any{"note": map[string]any{"type": "string"}}
	plain := map[string]any{"type": "object", "properties": plainProperties}

	params := sentToolParameters(t, []ToolDefinition{
		{Name: "pydantic", Parameters: pydantic},
		{Name: "legacy", Parameters: legacy},
		{Name: "cyclic", Parameters: cyclic},
		{Name: "plain", Parameters: plain},
	})

	wantItem := "map[properties:map[name:map[type:string]] required:[name] type:object]"
	pydanticProps := params["pydantic"]["properties"].(map[string]any)
	if got := fmt.Sprint(pydanticProps["items"].(map[string]any)["items"]); got != wantItem {
		t.Errorf("pydantic items.items = %s, want the inlined Item %s", got, wantItem)
	}
	owner := pydanticProps["owner"].(map[string]any)
	if owner["description"] != "who owns the order" || owner["type"] != "object" {
		t.Errorf("pydantic owner = %v, want the inlined Item carrying its own description", owner)
	}
	if _, present := params["pydantic"]["$defs"]; present {
		t.Errorf("pydantic must not forward $defs once every ref is inlined, got %v", params["pydantic"]["$defs"])
	}
	if got := fmt.Sprint(params["legacy"]["properties"].(map[string]any)["item"]); got != wantItem {
		t.Errorf("legacy item = %s, want the inlined Item %s", got, wantItem)
	}

	tree := params["cyclic"]["properties"].(map[string]any)["tree"].(map[string]any)
	child := tree["properties"].(map[string]any)["child"].(map[string]any)
	if child["$ref"] != "#/$defs/Node" {
		t.Errorf("cyclic tree.child = %v, want the self-reference left in place", child)
	}
	if _, ok := params["cyclic"]["$defs"].(map[string]any)["Node"]; !ok {
		t.Errorf("cyclic $defs = %v, want Node forwarded so the remaining ref resolves", params["cyclic"]["$defs"])
	}

	if got, want := fmt.Sprint(params["plain"]["properties"]), fmt.Sprint(plainProperties); got != want {
		t.Errorf("plain properties = %s, want unchanged %s", got, want)
	}
	if _, present := params["plain"]["$defs"]; present {
		t.Errorf("plain must not gain $defs, got %v", params["plain"]["$defs"])
	}

	if ref := pydantic["properties"].(map[string]any)["owner"].(map[string]any)["$ref"]; ref != "#/$defs/Item" {
		t.Errorf("caller's schema was mutated: owner $ref = %v", ref)
	}
	if _, present := pydantic["$defs"]; !present {
		t.Error("caller's schema was mutated: $defs removed")
	}
}

func TestChatCompletionForwardsDefinitionsForUninlinableToolSchemaRefs(t *testing.T) {
	deepPointer := map[string]any{
		"type":       "object",
		"properties": map[string]any{"b": map[string]any{"$ref": "#/$defs/A/properties/b"}},
		"$defs": map[string]any{
			"A": map[string]any{
				"type":       "object",
				"properties": map[string]any{"b": map[string]any{"type": "string"}},
			},
		},
	}
	booleanDef := map[string]any{
		"type":       "object",
		"properties": map[string]any{"anything": map[string]any{"$ref": "#/$defs/Any"}},
		"$defs":      map[string]any{"Any": true},
	}

	params := sentToolParameters(t, []ToolDefinition{
		{Name: "deep_pointer", Parameters: deepPointer},
		{Name: "boolean_def", Parameters: booleanDef},
	})

	b := params["deep_pointer"]["properties"].(map[string]any)["b"].(map[string]any)
	if b["$ref"] != "#/$defs/A/properties/b" {
		t.Errorf("deep_pointer b = %v, want the pointer into A left in place", b)
	}
	deepPointerDefs, _ := params["deep_pointer"]["$defs"].(map[string]any)
	if _, ok := deepPointerDefs["A"]; !ok {
		t.Errorf("deep_pointer $defs = %v, want A forwarded so the remaining ref resolves", params["deep_pointer"]["$defs"])
	}

	anything := params["boolean_def"]["properties"].(map[string]any)["anything"].(map[string]any)
	if anything["$ref"] != "#/$defs/Any" {
		t.Errorf("boolean_def anything = %v, want the ref to the boolean definition left in place", anything)
	}
	booleanDefs, _ := params["boolean_def"]["$defs"].(map[string]any)
	if got := booleanDefs["Any"]; got != true {
		t.Errorf("boolean_def $defs.Any = %v, want true forwarded so the remaining ref resolves", got)
	}
}

func TestConvertToolsInlinesSchemaRefsDeterministicallyPastInlineBound(t *testing.T) {
	properties := map[string]any{}
	for i := range maxSchemaRefInlines + 64 {
		properties[fmt.Sprintf("p%03d", i)] = map[string]any{"$ref": "#/$defs/Item"}
	}
	tool := ToolDefinition{
		Name: "fan_out",
		Parameters: map[string]any{
			"type":       "object",
			"properties": properties,
			"$defs": map[string]any{
				"Item": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			},
		},
	}

	var first []byte
	for i := range 20 {
		got, err := json.Marshal(convertTools([]ToolDefinition{tool})[0].Function.Parameters)
		if err != nil {
			t.Fatalf("marshal parameters: %v", err)
		}
		if i == 0 {
			first = got
			continue
		}
		if string(got) != string(first) {
			t.Fatalf("conversion %d differs from the first; which refs are inlined past the bound must not depend on map order", i)
		}
	}
}

func TestConvertToolsKeepsRefsWhenInliningWouldMultiplyTheSchema(t *testing.T) {
	refsTo := func(def string, n int) map[string]any {
		props := map[string]any{}
		for i := range n {
			props[fmt.Sprintf("f%d", i)] = map[string]any{"$ref": "#/$defs/" + def}
		}
		return props
	}
	leafProps := map[string]any{}
	for i := range 10 {
		leafProps[fmt.Sprintf("field%d", i)] = map[string]any{"type": "string", "description": "a described leaf field of the shared nested model"}
	}
	params := map[string]any{
		"type":       "object",
		"properties": refsTo("Mid", 8),
		"$defs": map[string]any{
			"Mid":  map[string]any{"type": "object", "properties": refsTo("Leaf", 8)},
			"Leaf": map[string]any{"type": "object", "properties": leafProps},
		},
	}

	got := convertedToolParameters(t, ToolDefinition{Name: "fan_out", Parameters: params})

	f0 := got["properties"].(map[string]any)["f0"].(map[string]any)
	if f0["$ref"] != "#/$defs/Mid" {
		t.Errorf("f0 = %v, want the $ref kept once inlining would multiply the schema", f0)
	}
	defs, _ := got["$defs"].(map[string]any)
	if _, ok := defs["Mid"]; !ok {
		t.Errorf("$defs = %v, want Mid forwarded so the kept refs resolve", got["$defs"])
	}
	if _, ok := defs["Leaf"]; !ok {
		t.Errorf("$defs = %v, want Leaf forwarded so the kept refs resolve", got["$defs"])
	}
}

func TestConvertToolsKeepsARefWhoseSiblingsConstrainTheShape(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"merged": map[string]any{
				"$ref":       "#/$defs/A",
				"properties": map[string]any{"extra": map[string]any{}},
				"required":   []any{"extra"},
			},
			"described": map[string]any{"$ref": "#/$defs/A", "description": "an annotated A"},
		},
		"$defs": map[string]any{
			"A": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x": map[string]any{"type": "string"},
					"y": map[string]any{"type": "string"},
				},
				"required": []any{"x", "y"},
			},
		},
	}

	got := convertedToolParameters(t, ToolDefinition{Name: "siblings", Parameters: params})

	props := got["properties"].(map[string]any)
	merged := props["merged"].(map[string]any)
	if merged["$ref"] != "#/$defs/A" {
		t.Errorf("merged = %v, want the $ref kept so A's x and y are not replaced by the sibling properties", merged)
	}
	if _, ok := got["$defs"].(map[string]any)["A"]; !ok {
		t.Errorf("$defs = %v, want A forwarded so the kept ref resolves", got["$defs"])
	}
	described := props["described"].(map[string]any)
	if described["description"] != "an annotated A" || described["type"] != "object" {
		t.Errorf("described = %v, want A inlined carrying its own description", described)
	}
}

func TestConvertToolsInlinesARootRefBesideSchemaMetadata(t *testing.T) {
	args := map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required":   []any{"q"},
	}
	cases := map[string]map[string]any{
		"$schema sibling": {
			"$ref":        "#/definitions/Args",
			"$schema":     "http://json-schema.org/draft-07/schema#",
			"definitions": map[string]any{"Args": args},
		},
		"type object sibling": {
			"type":  "object",
			"$ref":  "#/$defs/Args",
			"$defs": map[string]any{"Args": args},
		},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			got := convertedToolParameters(t, ToolDefinition{Name: "root_ref", Parameters: params})
			props, _ := got["properties"].(map[string]any)
			if _, found := props["q"]; !found {
				t.Errorf("parameters = %v, want the root $ref inlined so q survives", got)
			}
		})
	}
}

func convertedToolParameters(t *testing.T, tool ToolDefinition) map[string]any {
	t.Helper()
	raw, err := json.Marshal(convertTools([]ToolDefinition{tool})[0].Function.Parameters)
	if err != nil {
		t.Fatalf("marshal parameters: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode parameters: %v", err)
	}
	return got
}

func sentToolParameters(t *testing.T, tools []ToolDefinition) map[string]map[string]any {
	t.Helper()
	var body []byte
	respond := nonStreamHandler("ok", "stop")
	server := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		respond(w, r)
	})
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.ChatCompletion(t.Context(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatal(err)
	}

	var sent struct {
		Tools []struct {
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	params := map[string]map[string]any{}
	for _, tool := range sent.Tools {
		params[tool.Function.Name] = tool.Function.Parameters
	}
	return params
}

func TestConvertToolsDeterministicOrder(t *testing.T) {
	tool := ToolDefinition{
		Name:        "test",
		Description: "test tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"zebra":  map[string]any{"type": "string"},
				"apple":  map[string]any{"type": "string"},
				"mango":  map[string]any{"type": "string"},
				"banana": map[string]any{"type": "string"},
			},
			"required": []any{"apple", "zebra"},
		},
	}

	converted := convertTools([]ToolDefinition{tool})
	props := converted[0].Function.Parameters.Properties

	keys := props.Keys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Errorf("properties not sorted: %v", keys)
			break
		}
	}
}

func TestStreamContextCancellation(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		for range 30 {
			data := map[string]any{
				"id": "chatcmpl-cancel", "object": "chat.completion.chunk",
				"model": "test-model",
				"choices": []map[string]any{
					{"index": 0, "finish_reason": nil, "delta": map[string]any{"content": "x"}},
				},
			}
			jsonBytes, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonBytes)
			flusher.Flush()
		}

		finalData := map[string]any{
			"id": "chatcmpl-cancel", "object": "chat.completion.chunk",
			"model": "test-model",
			"choices": []map[string]any{
				{"index": 0, "finish_reason": "stop", "delta": map[string]any{"content": ""}},
			},
		}
		jsonBytes, _ := json.Marshal(finalData)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonBytes)
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	server := mockOpenAIServer(t, handler)
	defer server.Close()

	client := newTestClient(t, server.URL)

	ctx, cancel := context.WithCancel(t.Context())

	stream, err := client.ChatCompletionStream(ctx, &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	chunk, ok := <-stream
	if !ok {
		t.Fatal("stream closed before first chunk")
	}
	if chunk.Err != nil {
		t.Fatal(chunk.Err)
	}

	cancel()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-stream:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("stream did not close after context cancellation")
		}
	}
}

func TestToBifrostCacheControlUsesType(t *testing.T) {
	cc := &CacheControl{Type: "ephemeral"}
	sc := toBifrostCacheControl(cc)
	if string(sc.Type) != "ephemeral" {
		t.Errorf("Type = %q, want %q", sc.Type, "ephemeral")
	}

	cc2 := &CacheControl{}
	sc2 := toBifrostCacheControl(cc2)
	if string(sc2.Type) != "ephemeral" {
		t.Errorf("Type = %q, want %q", sc2.Type, "ephemeral")
	}
}

func TestExtraHeadersPassthrough(t *testing.T) {
	var capturedHeaders http.Header

	server := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer server.Close()

	t.Setenv("OPENAI_API_KEY", "test-key-123")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  server.URL,
		ExtraHeaders: map[string]string{
			"x-custom-header": "test-value-123",
			"x-another":       "second-value",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := capturedHeaders.Get("X-Custom-Header"); got != "test-value-123" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "test-value-123")
	}
	if got := capturedHeaders.Get("X-Another"); got != "second-value" {
		t.Errorf("X-Another = %q, want %q", got, "second-value")
	}
	if got := capturedHeaders.Get("Authorization"); got == "" {
		t.Error("expected Authorization header to be present")
	}
	if got := capturedHeaders.Get("Content-Type"); got == "" {
		t.Error("expected Content-Type header to be present")
	}
}

func skipUnlessIntegration(t *testing.T) BifrostConfig {
	t.Helper()
	url := os.Getenv("LLM_SERVICE_URL")
	key := os.Getenv("LLM_SERVICE_API_KEY")
	model := os.Getenv("LLM_SERVICE_MODEL")
	if url == "" || key == "" || model == "" {
		t.Skip("set LLM_SERVICE_URL, LLM_SERVICE_API_KEY, LLM_SERVICE_MODEL for integration tests")
	}
	return BifrostConfig{
		Provider:     "openai",
		Model:        model,
		BaseURL:      url,
		APIKeyEnvVar: "LLM_SERVICE_API_KEY",
	}
}

func TestIntegrationNonStream(t *testing.T) {
	cfg := skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	resp, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Reply with exactly: PONG"},
		},
		MaxTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Content, "PONG") {
		t.Errorf("content = %q, expected PONG", resp.Content)
	}
	t.Logf("Response: %q, usage: %+v", resp.Content, resp.Usage)
}

func TestIntegrationStream(t *testing.T) {
	cfg := skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	stream, err := client.ChatCompletionStream(ctx, &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Reply with exactly: STREAM-OK"},
		},
		MaxTokens: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		content.WriteString(chunk.ContentDelta)
	}
	if !strings.Contains(content.String(), "STREAM-OK") {
		t.Errorf("stream content = %q, expected STREAM-OK", content.String())
	}
	t.Logf("Streamed: %q", content.String())
}

func TestIntegrationToolCalling(t *testing.T) {
	cfg := skipUnlessIntegration(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := NewBifrostClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Shutdown()

	calcTool := ToolDefinition{
		Name:        "calculator",
		Description: "Perform basic arithmetic",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{"type": "string", "enum": []string{"add", "subtract", "multiply", "divide"}},
				"a":         map[string]any{"type": "number"},
				"b":         map[string]any{"type": "number"},
			},
			"required": []any{"operation", "a", "b"},
		},
	}

	resp, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 42 * 17? Use the calculator tool."},
		},
		Tools:      []ToolDefinition{calcTool},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.ToolCalls) == 0 {
		t.Logf("Model answered directly without tool call: %q", resp.Content)
		return
	}

	tc := resp.ToolCalls[0]
	t.Logf("Tool call: %s(%s)", tc.Name, tc.Arguments)

	resp2, err := client.ChatCompletion(ctx, &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "What is 42 * 17? Use the calculator tool."},
			{Role: "assistant", ToolCalls: resp.ToolCalls},
			{Role: "tool", Content: "714", ToolCallID: tc.ID},
		},
		Tools: []ToolDefinition{calcTool},
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(resp2.Content, "714") {
		t.Errorf("final response = %q, expected 714", resp2.Content)
	}
	t.Logf("Final: %q", resp2.Content)
}

func reasoningNonStreamHandler(reasoning, content, finishReason string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		resp := map[string]any{
			"id":      "chatcmpl-reason",
			"object":  "chat.completion",
			"created": 1234567890,
			"model":   "test-model",
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": finishReason,
					"message": map[string]any{
						"role":      "assistant",
						"content":   content,
						"reasoning": reasoning,
					},
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 15,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestChatCompletionWithReasoning(t *testing.T) {
	server := mockOpenAIServer(t, reasoningNonStreamHandler(
		"Let me think step by step...",
		"The answer is 42.",
		"stop",
	))
	defer server.Close()

	client := newTestClient(t, server.URL)

	resp, err := client.ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "What is the answer?"},
		},
		Reasoning: &ReasoningConfig{BudgetTokens: 10000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "The answer is 42." {
		t.Errorf("content = %q, want %q", resp.Content, "The answer is 42.")
	}
	if resp.ReasoningContent != "Let me think step by step..." {
		t.Errorf("reasoning_content = %q, want %q", resp.ReasoningContent, "Let me think step by step...")
	}
	if resp.Usage == nil {
		t.Fatal("expected usage")
	}
	if resp.Usage.ReasoningTokens != 15 {
		t.Errorf("reasoning_tokens = %d, want 15", resp.Usage.ReasoningTokens)
	}
}

func reasoningStreamHandler(reasoningChunks, contentChunks []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		for _, chunk := range reasoningChunks {
			data := map[string]any{
				"id": "chatcmpl-stream-r", "object": "chat.completion.chunk",
				"model": "test-model",
				"choices": []map[string]any{
					{"index": 0, "finish_reason": nil, "delta": map[string]any{"reasoning": chunk}},
				},
			}
			jsonBytes, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonBytes)
			flusher.Flush()
		}

		for i, chunk := range contentChunks {
			finishReason := any(nil)
			if i == len(contentChunks)-1 {
				finishReason = "stop"
			}
			data := map[string]any{
				"id": "chatcmpl-stream-r", "object": "chat.completion.chunk",
				"model": "test-model",
				"choices": []map[string]any{
					{"index": 0, "finish_reason": finishReason, "delta": map[string]any{"content": chunk}},
				},
			}
			if i == len(contentChunks)-1 {
				data["usage"] = map[string]any{
					"prompt_tokens": 10, "completion_tokens": 25, "total_tokens": 35,
					"completion_tokens_details": map[string]any{"reasoning_tokens": 12},
				}
			}
			jsonBytes, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", jsonBytes)
			flusher.Flush()
		}

		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func TestChatCompletionStreamWithReasoning(t *testing.T) {
	server := mockOpenAIServer(t, reasoningStreamHandler(
		[]string{"Let me ", "think..."},
		[]string{"The answer ", "is 42."},
	))
	defer server.Close()

	client := newTestClient(t, server.URL)

	stream, err := client.ChatCompletionStream(context.Background(), &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Question"},
		},
		Reasoning: &ReasoningConfig{BudgetTokens: 10000},
	})
	if err != nil {
		t.Fatal(err)
	}

	var reasoning, content strings.Builder
	var lastChunk StreamChunk
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatal(chunk.Err)
		}
		reasoning.WriteString(chunk.ReasoningDelta)
		content.WriteString(chunk.ContentDelta)
		lastChunk = chunk
	}

	if reasoning.String() != "Let me think..." {
		t.Errorf("reasoning = %q, want %q", reasoning.String(), "Let me think...")
	}
	if content.String() != "The answer is 42." {
		t.Errorf("content = %q, want %q", content.String(), "The answer is 42.")
	}
	if lastChunk.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", lastChunk.FinishReason, "stop")
	}
	if lastChunk.Usage == nil {
		t.Fatal("expected usage on final chunk")
	}
	if lastChunk.Usage.ReasoningTokens != 12 {
		t.Errorf("reasoning_tokens = %d, want 12", lastChunk.Usage.ReasoningTokens)
	}
}

func TestBuildRequestWithReasoning(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	client := &BifrostClient{cfg: BifrostConfig{Provider: "openai", Model: "test"}}

	req := &ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
		Reasoning: &ReasoningConfig{
			BudgetTokens: 8192,
		},
	}
	bfReq := mustBuildRequest(t, client, req)
	if bfReq.Params == nil {
		t.Fatal("expected params to be set")
	}
	if bfReq.Params.Reasoning == nil {
		t.Fatal("expected reasoning to be set")
	}
	if bfReq.Params.Reasoning.MaxTokens == nil || *bfReq.Params.Reasoning.MaxTokens != 8192 {
		t.Errorf("reasoning.max_tokens = %v, want 8192", bfReq.Params.Reasoning.MaxTokens)
	}
}

func TestBuildRequestWithoutReasoning(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	client := &BifrostClient{cfg: BifrostConfig{Provider: "openai", Model: "test"}}

	req := &ChatRequest{
		Model:    "test-model",
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	}
	bfReq := mustBuildRequest(t, client, req)
	if bfReq.Params != nil {
		if bfReq.Params.Reasoning != nil {
			t.Error("reasoning should not be set when not configured")
		}
	}
}
