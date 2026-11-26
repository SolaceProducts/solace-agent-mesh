// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/maximhq/bifrost/core/schemas"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
)

const openAIChatCompletionStub = `{
	"id": "chatcmpl-test",
	"object": "chat.completion",
	"created": 1,
	"model": "thinking-model",
	"choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}],
	"usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
}`

func toolLoopHistory(reasoning string) []ChatMessage {
	return []ChatMessage{
		{Role: "user", Content: "Build the report"},
		{Role: "assistant", Reasoning: reasoning, ToolCalls: []ToolCall{{ID: "call-1", Name: "lookup", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "call-1", ToolName: "lookup", Content: "found"},
	}
}

func captureRequestBody(t *testing.T, provider string, req *ChatRequest) map[string]any {
	t.Helper()
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "test-key")

	bodies := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		select {
		case bodies <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openAIChatCompletionStub))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   provider,
		Model:      "thinking-model",
		BaseURL:    srv.URL,
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatalf("NewBifrostClient(provider %q): %v", provider, err)
	}
	t.Cleanup(client.Shutdown)

	if _, err := client.ChatCompletion(ctx, req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	var sent map[string]any
	select {
	case raw := <-bodies:
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatalf("unmarshal captured body: %v\n%s", err, raw)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no request reached the stand-in API")
	}
	return sent
}

func captureToolLoopAssistantTurn(t *testing.T, provider string) map[string]any {
	t.Helper()
	sent := captureRequestBody(t, provider, &ChatRequest{Messages: toolLoopHistory("I need the lookup tool."), MaxTokens: 256})
	messages, _ := sent["messages"].([]any)
	for _, m := range messages {
		if turn, ok := m.(map[string]any); ok && turn["role"] == "assistant" {
			return turn
		}
	}
	t.Fatalf("no assistant message in the request: %+v", sent["messages"])
	return nil
}

func TestToolCallTurnWithoutReasoningTurnsThinkingOffEvenWithNoOtherParameters(t *testing.T) {
	sent := captureRequestBody(t, "deepseek", &ChatRequest{Messages: toolLoopHistory("")})
	want := map[string]any{"type": "disabled"}
	if diff := cmp.Diff(want, sent["thinking"]); diff != "" {
		t.Errorf("thinking in a request with no parameters over a tool-call turn without reasoning (-want +got):\n%s", diff)
	}
}

func TestToolCallReasoningOnTheWire(t *testing.T) {
	cases := []struct {
		provider string
		want     any
	}{
		{provider: "deepseek", want: "I need the lookup tool."},
		{provider: "openai", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			turn := captureToolLoopAssistantTurn(t, tc.provider)
			if got := turn["reasoning_content"]; got != tc.want {
				t.Errorf("reasoning_content = %v, want %v", got, tc.want)
			}
			if got, present := turn["reasoning"]; present {
				t.Errorf("reasoning = %v, want the field absent", got)
			}
		})
	}
}

func TestOpenAIProviderAtANativeHostSendsReasoningOnTheNativeRoute(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "openai",
		Model:      "thinking-model",
		BaseURL:    "https://api.deepseek.com",
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatalf("NewBifrostClient(openai at a native provider host): %v", err)
	}
	t.Cleanup(client.Shutdown)

	bfReq, err := client.buildRequest(&ChatRequest{Messages: toolLoopHistory("I need the lookup tool.")})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	type wire struct {
		Provider  schemas.ModelProvider
		Reasoning *string
	}
	want := wire{Provider: schemas.DeepSeek, Reasoning: new("I need the lookup tool.")}
	got := wire{Provider: bfReq.Provider}
	if turn := bfReq.Input[1]; turn.ChatAssistantMessage != nil {
		got.Reasoning = turn.Reasoning
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("request for the tool-call turn (-want +got):\n%s", diff)
	}
}

func TestOverrideOfARoutedClientUsesItsOwnProvidersKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")

	keys := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case keys <- r.Header.Get("x-api-key"):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-4-5",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{
		Provider:   "openai",
		Model:      "thinking-model",
		BaseURL:    "https://api.deepseek.com",
		MaxRetries: new(0),
	})
	if err != nil {
		t.Fatalf("NewBifrostClient(openai at a native provider host): %v", err)
	}
	t.Cleanup(client.Shutdown)

	_, err = client.ChatCompletion(ctx, &ChatRequest{
		Messages:      []ChatMessage{{Role: "user", Content: "Hi"}},
		MaxTokens:     256,
		ModelOverride: &BifrostConfig{Provider: "anthropic", Model: "claude-sonnet-4-5", BaseURL: srv.URL},
	})
	if err != nil {
		t.Fatalf("ChatCompletion with an anthropic override: %v", err)
	}
	select {
	case got := <-keys:
		if got != "anthropic-key" {
			t.Errorf("x-api-key = %q, want the override provider's own key %q", got, "anthropic-key")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no request reached the stand-in API")
	}
}

func TestRouteOpenAICompatibleHostToNativeProvider(t *testing.T) {
	type routed struct {
		Provider     string
		APIKeyEnvVar string
		BaseURL      string
	}
	cases := []struct {
		name string
		cfg  BifrostConfig
		want routed
	}{
		{
			name: "provider's own API host keeps the OpenAI key first",
			cfg:  BifrostConfig{Provider: "openai", BaseURL: "https://api.deepseek.com"},
			want: routed{Provider: "deepseek", APIKeyEnvVar: "OPENAI_API_KEY", BaseURL: "https://api.deepseek.com"},
		},
		{
			name: "configured key and v1 path are kept",
			cfg:  BifrostConfig{Provider: "openai", BaseURL: "https://api.deepseek.com/v1", APIKeyEnvVar: "SAM_KEY"},
			want: routed{Provider: "deepseek", APIKeyEnvVar: "SAM_KEY", BaseURL: "https://api.deepseek.com/v1"},
		},
		{
			name: "full chat completions endpoint is cut back to its root",
			cfg:  BifrostConfig{Provider: "openai", BaseURL: "https://api.deepseek.com/v1/chat/completions"},
			want: routed{Provider: "deepseek", APIKeyEnvVar: "OPENAI_API_KEY", BaseURL: "https://api.deepseek.com/v1"},
		},
		{
			name: "other OpenAI-compatible host is untouched",
			cfg:  BifrostConfig{Provider: "openai", BaseURL: "https://litellm.example.com"},
			want: routed{Provider: "openai", BaseURL: "https://litellm.example.com"},
		},
		{
			name: "grant-authenticated endpoint is untouched",
			cfg:  BifrostConfig{Provider: "openai", BaseURL: "https://api.deepseek.com", GrantID: "grant-1"},
			want: routed{Provider: "openai", BaseURL: "https://api.deepseek.com"},
		},
		{
			name: "OAuth2-authenticated endpoint is untouched",
			cfg:  BifrostConfig{Provider: "openai", BaseURL: "https://api.deepseek.com", OAuth2: &oauth2cc.Config{}},
			want: routed{Provider: "openai", BaseURL: "https://api.deepseek.com"},
		},
		{
			name: "non-OpenAI provider is untouched",
			cfg:  BifrostConfig{Provider: "anthropic", BaseURL: "https://api.deepseek.com"},
			want: routed{Provider: "anthropic", BaseURL: "https://api.deepseek.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := routeOpenAICompatibleHostToNativeProvider(tc.cfg, slog.New(slog.DiscardHandler))
			if diff := cmp.Diff(tc.want, routed{Provider: out.Provider, APIKeyEnvVar: out.APIKeyEnvVar, BaseURL: out.BaseURL}); diff != "" {
				t.Errorf("routed config (-want +got):\n%s", diff)
			}
		})
	}
}
