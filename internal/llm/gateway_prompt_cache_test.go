// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestClaudeBehindOpenAIGateway(t *testing.T) {
	openai := func(base string) BifrostConfig { return BifrostConfig{Provider: "openai", BaseURL: base} }
	for _, tc := range []struct {
		name  string
		cfg   BifrostConfig
		azure azureGatewayRoute
		want  bool
	}{
		{"openai behind a gateway", openai("https://litellm.internal:4000/v1"), azureGatewayRoute{}, true},
		{"openai at its own host", openai("https://api.openai.com/v1"), azureGatewayRoute{}, false},
		{"openai apex host", openai("https://openai.com/v1"), azureGatewayRoute{}, false},
		{"openai regional subdomain", openai("https://eu.api.openai.com/v1"), azureGatewayRoute{}, false},
		{"openai host in other case with a trailing dot", openai("https://API.OpenAI.com./v1"), azureGatewayRoute{}, false},
		{"openai host with a port", openai("https://api.openai.com:443/v1"), azureGatewayRoute{}, false},
		{"lookalike host", openai("https://notopenai.com/v1"), azureGatewayRoute{}, true},
		{"openai host as userinfo in front of a gateway", openai("https://api.openai.com@litellm.internal/v1"), azureGatewayRoute{}, true},
		{"openai with no base url", openai(""), azureGatewayRoute{}, false},
		{"openai with a scheme-less base url", openai("litellm.internal:4000"), azureGatewayRoute{}, false},
		{"azure gateway presented as openai", openai("https://gateway.corp/azure"), azureGatewayRoute{active: true}, false},
		{"openai behind a gateway called with a grant", BifrostConfig{Provider: "openai", BaseURL: "https://free-llm.example/v1", GrantID: "grant-1"}, azureGatewayRoute{}, false},
		{"anthropic behind a gateway", BifrostConfig{Provider: "anthropic", BaseURL: "https://litellm.internal:4000"}, azureGatewayRoute{}, false},
		{"groq", BifrostConfig{Provider: "groq", BaseURL: "https://api.groq.com"}, azureGatewayRoute{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeBehindOpenAIGateway(tc.cfg, tc.azure); got != tc.want {
				t.Fatalf("claudeBehindOpenAIGateway = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWireProviderFor(t *testing.T) {
	gateway := &BifrostClient{cfg: BifrostConfig{Provider: "openai"}, claudeBehindGateway: true}
	direct := &BifrostClient{cfg: BifrostConfig{Provider: "openai"}}
	for _, tc := range []struct {
		name   string
		client *BifrostClient
		model  string
		want   schemas.ModelProvider
	}{
		{"claude through a gateway", gateway, "bedrock-claude-4-5-sonnet-tools", schemas.OpenRouter},
		{"bedrock-style id through a gateway", gateway, "anthropic.claude-3-5-sonnet-20241022-v2:0", schemas.OpenRouter},
		{"gateway alias naming the tier", gateway, "sonnet-prod", schemas.OpenRouter},
		{"gateway alias naming another tier", gateway, "corp_OPUS_default", schemas.OpenRouter},
		{"newer tier", gateway, "claude-fable-5-1", schemas.OpenRouter},
		{"gpt through a gateway", gateway, "gpt-5.6", schemas.OpenAI},
		{"open-weight model through a gateway", gateway, "llama-3.3-70b", schemas.OpenAI},
		{"tier word inside another word", gateway, "octopus-8b", schemas.OpenAI},
		{"claude named model at openai itself", direct, "claude-sonnet-5", schemas.OpenAI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.client.wireProviderFor(tc.model); got != tc.want {
				t.Fatalf("wireProviderFor = %q, want %q", got, tc.want)
			}
		})
	}
}

type capturingGateway struct {
	mu      sync.Mutex
	bodies  [][]byte
	auths   []string
	respond func(w http.ResponseWriter, call int)
}

func (g *capturingGateway) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		g.bodies = append(g.bodies, body)
		g.auths = append(g.auths, r.Header.Get("Authorization"))
		call := len(g.bodies)
		g.mu.Unlock()
		g.respond(w, call)
	}
}

func (g *capturingGateway) raw(t *testing.T, i int) (body []byte, auth string) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if i >= len(g.bodies) {
		t.Fatalf("only %d requests captured, want index %d", len(g.bodies), i)
	}
	return g.bodies[i], g.auths[i]
}

func (g *capturingGateway) body(t *testing.T, i int) map[string]any {
	t.Helper()
	raw, _ := g.raw(t, i)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("request %d is not JSON: %v", i, err)
	}
	return m
}

func chatCompletionJSON(w http.ResponseWriter, promptTokens, cachedTokens int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "claude-sonnet-5",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "ok"}}},
		"usage": map[string]any{
			"prompt_tokens": promptTokens, "completion_tokens": 2, "total_tokens": promptTokens + 2,
			"prompt_tokens_details": map[string]any{"cached_tokens": cachedTokens},
		},
	})
}

func cachedUsageFrame(promptTokens, cachedTokens int) sseFrame {
	return sseFrame{
		"id": "chatcmpl-stream", "object": "chat.completion.chunk", "created": 1, "model": "claude-sonnet-5",
		"choices": []map[string]any{{"index": 0, "finish_reason": nil, "delta": map[string]any{}}},
		"usage": map[string]any{"prompt_tokens": promptTokens, "completion_tokens": 1, "total_tokens": promptTokens + 1,
			"prompt_tokens_details": map[string]any{"cached_tokens": cachedTokens}},
	}
}

func markedRequest(model string) *ChatRequest {
	return &ChatRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "system", Content: "You are a careful assistant.", CacheControl: &CacheControl{Type: "ephemeral"}},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "and again"},
		},
		Tools: []ToolDefinition{{
			Name:         "get_time",
			Description:  "current time",
			Parameters:   map[string]any{"type": "object"},
			CacheControl: &CacheControl{Type: "ephemeral"},
		}},
	}
}

func firstTurnRequest() *ChatRequest {
	req := markedRequest("")
	req.Messages = req.Messages[:2]
	return req
}

func newGatewayClient(t *testing.T, gw *capturingGateway, model string, logger *slog.Logger) *BifrostClient {
	t.Helper()
	srv := httptest.NewServer(gw.handler())
	t.Cleanup(srv.Close)
	t.Setenv("OPENAI_API_KEY", "gateway-key-123")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{Provider: "openai", Model: model, BaseURL: srv.URL + "/v1", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func cacheControlOnLastBlock(msg map[string]any) bool {
	blocks, ok := msg["content"].([]any)
	if !ok || len(blocks) == 0 {
		return false
	}
	last, _ := blocks[len(blocks)-1].(map[string]any)
	_, has := last["cache_control"]
	return has
}

func TestOpenAICompatibleGatewayKeepsCacheMarkers(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 5000, 4000) }}
	client := newGatewayClient(t, gw, "claude-sonnet-5", nil)

	cr, err := client.ChatCompletion(t.Context(), markedRequest(""))
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	raw, auth := gw.raw(t, 0)
	body := gw.body(t, 0)
	msgs := body["messages"].([]any)
	if !cacheControlOnLastBlock(msgs[0].(map[string]any)) {
		t.Errorf("system message lost its cache_control marker: %s", raw)
	}
	tools := body["tools"].([]any)
	if _, has := tools[0].(map[string]any)["cache_control"]; !has {
		t.Errorf("tool lost its cache_control marker: %s", raw)
	}
	if auth != "Bearer gateway-key-123" {
		t.Errorf("Authorization = %q, want the configured openai key", auth)
	}
	if got := body["model"]; got != "claude-sonnet-5" {
		t.Errorf("model = %v, want it untouched", got)
	}
	if cr.Usage == nil || cr.Usage.PromptTokens != 5000 || cr.Usage.CachedTokens != 4000 {
		t.Errorf("usage = %+v, want the gateway's prompt and cached counts parsed on this route", cr.Usage)
	}
}

func TestOpenAICompatibleGatewayStreamKeepsCacheMarkers(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) {
		streamHandler([]string{"ok"})(w, nil)
	}}
	client := newGatewayClient(t, gw, "claude-sonnet-5", nil)

	stream, err := client.ChatCompletionStream(t.Context(), markedRequest(""))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	for range stream {
	}

	raw, _ := gw.raw(t, 0)
	msgs := gw.body(t, 0)["messages"].([]any)
	if !cacheControlOnLastBlock(msgs[0].(map[string]any)) {
		t.Errorf("system message lost its cache_control marker: %s", raw)
	}
}

func TestOpenAICompatibleGatewayKeepsOpenAIRouteForOtherModels(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 10, 0) }}
	client := newGatewayClient(t, gw, "gpt-5.6", nil)

	req := markedRequest("")
	req.Temperature = schemas.Ptr(0.2)
	req.Reasoning = &ReasoningConfig{Effort: "low"}
	if _, err := client.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	raw, _ := gw.raw(t, 0)
	if bytes.Contains(raw, []byte("cache_control")) {
		t.Errorf("a GPT request through a gateway carried cache_control, which OpenAI rejects: %s", raw)
	}
	if bytes.Contains(raw, []byte(`"temperature"`)) {
		t.Errorf("Bifrost's OpenAI route did not strip temperature for a reasoning model, so the request took another route: %s", raw)
	}
}

func TestOpenAICompatibleGatewayRoutesPerModelOnOneClient(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 10, 0) }}
	client := newGatewayClient(t, gw, "gpt-5.6", nil)

	for _, model := range []string{"claude-sonnet-5", "gpt-5.6", "claude-sonnet-5"} {
		if _, err := client.ChatCompletion(t.Context(), markedRequest(model)); err != nil {
			t.Fatalf("%s: %v", model, err)
		}
	}

	for i, wantMarkers := range []bool{true, false, true} {
		raw, auth := gw.raw(t, i)
		if got := bytes.Contains(raw, []byte("cache_control")); got != wantMarkers {
			t.Errorf("request %d carried markers = %v, want %v: %s", i, got, wantMarkers, raw)
		}
		if auth != "Bearer gateway-key-123" {
			t.Errorf("request %d Authorization = %q", i, auth)
		}
	}
}

func TestGrantCalledClaudeNamedModelKeepsTheOpenAIRoute(t *testing.T) {
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 10, 0) }}
	srv := httptest.NewServer(gw.handler())
	t.Cleanup(srv.Close)
	t.Setenv("OPENAI_API_KEY", "")
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider: "openai",
		Model:    "claude-haiku-free",
		BaseURL:  srv.URL + "/v1",
		GrantID:  "grant-1",
		Grants:   &fakeGrants{},
	})
	if err != nil {
		t.Fatalf("NewBifrostClient: %v", err)
	}
	t.Cleanup(client.Shutdown)

	if _, err := client.ChatCompletion(t.Context(), markedRequest("")); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	raw, auth := gw.raw(t, 0)
	if bytes.Contains(raw, []byte("cache_control")) {
		t.Errorf("a grant-called Claude-named model took the OpenRouter cache route: %s", raw)
	}
	if got := gw.body(t, 0)["model"]; got != "claude-haiku-free" {
		t.Errorf("model = %v, want it untouched", got)
	}
	if auth != "Bearer token-1" {
		t.Errorf("Authorization = %q, want the grant's token", auth)
	}
}

func TestOpenAICompatibleGatewayErrorKeepsConfiguredProviderLabel(t *testing.T) {
	t.Run("rejected request", func(t *testing.T) {
		gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error"}}`))
		}}
		client := newGatewayClient(t, gw, "claude-sonnet-5", nil)

		_, err := client.ChatCompletion(t.Context(), markedRequest(""))
		var llmErr *Error
		if !errors.As(err, &llmErr) {
			t.Fatalf("err = %T %v, want *Error", err, err)
		}
		if llmErr.Provider != "openai" {
			t.Errorf("Provider = %q, want the configured provider", llmErr.Provider)
		}
	})
	t.Run("error inside the stream", func(t *testing.T) {
		gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) {
			sseFramesHandler(contentFrame("partial", ""), sseFrame{
				"error": map[string]any{"message": "upstream exploded", "type": "server_error"},
			})(w, nil)
		}}
		client := newGatewayClient(t, gw, "claude-sonnet-5", nil)

		stream, err := client.ChatCompletionStream(t.Context(), markedRequest(""))
		if err != nil {
			t.Fatalf("ChatCompletionStream: %v", err)
		}
		var streamErr error
		for chunk := range stream {
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
		}
		var llmErr *Error
		if !errors.As(streamErr, &llmErr) {
			t.Fatalf("stream error = %T %v, want *Error", streamErr, streamErr)
		}
		if llmErr.Provider != "openai" {
			t.Errorf("Provider = %q, want the configured provider", llmErr.Provider)
		}
	})
}

func TestPromptCacheWatch(t *testing.T) {
	miss := &Usage{PromptTokens: 5000}
	hit := &Usage{PromptTokens: 5000, CachedTokens: 4000}
	t.Run("warns once per run of misses and announces recovery", func(t *testing.T) {
		var w promptCacheWatch
		for i := 1; i < promptCacheMissesBeforeWarn; i++ {
			if _, event := w.observe(miss); event != promptCacheQuiet {
				t.Fatalf("event %v at miss %d", event, i)
			}
		}
		if misses, event := w.observe(miss); event != promptCacheNeverServed || misses != promptCacheMissesBeforeWarn {
			t.Fatalf("observe = (%d, %v), want (%d, never served)", misses, event, promptCacheMissesBeforeWarn)
		}
		if _, event := w.observe(miss); event != promptCacheQuiet {
			t.Fatalf("warned a second time: %v", event)
		}
		if misses, event := w.observe(hit); event != promptCacheServing || misses != 0 {
			t.Fatalf("recovery = (%d, %v), want (0, serving)", misses, event)
		}
		for i := 1; i < promptCacheMissesBeforeWarn; i++ {
			w.observe(miss)
		}
		if _, event := w.observe(miss); event != promptCacheNeverServed {
			t.Fatalf("a run of misses after recovery did not warn again: %v", event)
		}
	})
	t.Run("announces serving once when it was never in doubt", func(t *testing.T) {
		var w promptCacheWatch
		if _, event := w.observe(hit); event != promptCacheServing {
			t.Fatalf("first read = %v, want serving", event)
		}
		if _, event := w.observe(hit); event != promptCacheQuiet {
			t.Fatalf("second read = %v, want quiet", event)
		}
	})
	t.Run("a write-only call is not a read", func(t *testing.T) {
		var w promptCacheWatch
		writeOnly := &Usage{PromptTokens: 5000, CachedTokens: 4000, CacheWriteTokens: 4000}
		if misses, event := w.observe(writeOnly); misses != 1 || event != promptCacheQuiet {
			t.Fatalf("observe = (%d, %v), want a miss", misses, event)
		}
	})
	t.Run("ignores prompts too short to cache", func(t *testing.T) {
		var w promptCacheWatch
		for i := 0; i < 3*promptCacheMissesBeforeWarn; i++ {
			if _, event := w.observe(&Usage{PromptTokens: promptCacheMinPromptTokens - 1}); event != promptCacheQuiet {
				t.Fatal("reacted to a prompt below the cacheable minimum")
			}
		}
	})
}

func TestExpectsCacheRead(t *testing.T) {
	if expectsCacheRead(firstTurnRequest()) {
		t.Error("a first turn cannot be a cache read")
	}
	if !expectsCacheRead(markedRequest("")) {
		t.Error("a follow-up turn with markers should expect a read")
	}
	toolsOnly := markedRequest("")
	for i := range toolsOnly.Messages {
		toolsOnly.Messages[i].CacheControl = nil
	}
	if !expectsCacheRead(toolsOnly) {
		t.Error("a marker on a tool alone is still a request for caching")
	}
	unmarked := markedRequest("")
	for i := range unmarked.Messages {
		unmarked.Messages[i].CacheControl = nil
	}
	unmarked.Tools = nil
	if expectsCacheRead(unmarked) {
		t.Error("a request without markers expects nothing")
	}
}

const neverServedMessage = "prompt caching requested on every follow-up call but the provider never reported a cached token"

func TestPromptCacheNeverServedWarnsOnce(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 5000, 0) }}
	client := newGatewayClient(t, gw, "claude-sonnet-5", logger)

	for i := 0; i < promptCacheMissesBeforeWarn+5; i++ {
		if _, err := client.ChatCompletion(t.Context(), markedRequest("")); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if n := strings.Count(logs.String(), neverServedMessage); n != 1 {
		t.Fatalf("warning logged %d times, want once:\n%s", n, logs.String())
	}
	for _, field := range []string{"provider=openai", "host=127.0.0.1", "model=claude-sonnet-5", fmt.Sprintf("consecutiveMisses=%d", promptCacheMissesBeforeWarn), "hint="} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("warning lacks %s:\n%s", field, logs.String())
		}
	}
}

func TestPromptCacheNeverServedWarnsOnStreams(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) {
		sseFramesHandler(contentFrame("ok", "stop"), cachedUsageFrame(5000, 0))(w, nil)
	}}
	client := newGatewayClient(t, gw, "claude-sonnet-5", logger)

	for i := 0; i < promptCacheMissesBeforeWarn+5; i++ {
		stream, err := client.ChatCompletionStream(t.Context(), markedRequest(""))
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		for chunk := range stream {
			if chunk.Err != nil {
				t.Fatalf("call %d stream error: %v", i, chunk.Err)
			}
		}
	}

	if n := strings.Count(logs.String(), neverServedMessage); n != 1 {
		t.Fatalf("warning logged %d times, want once:\n%s", n, logs.String())
	}
}

func TestPromptCacheWarningStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name          string
		model         string
		cached        int
		firstTurnOnly bool
	}{
		{name: "served", model: "claude-sonnet-5", cached: 4500},
		{name: "not a claude model", model: "gpt-5.6"},
		{name: "first turns only", model: "claude-sonnet-5", firstTurnOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
			gw := &capturingGateway{respond: func(w http.ResponseWriter, _ int) { chatCompletionJSON(w, 5000, tc.cached) }}
			client := newGatewayClient(t, gw, tc.model, logger)

			for i := 0; i < promptCacheMissesBeforeWarn+5; i++ {
				req := markedRequest("")
				if tc.firstTurnOnly {
					req = firstTurnRequest()
				}
				if _, err := client.ChatCompletion(t.Context(), req); err != nil {
					t.Fatalf("call %d: %v", i, err)
				}
			}
			if strings.Contains(logs.String(), neverServedMessage) {
				t.Fatalf("unexpected warning:\n%s", logs.String())
			}
		})
	}
}

func TestPromptCacheWatchIsOffWithoutAGateway(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := &BifrostClient{cfg: BifrostConfig{Provider: "anthropic"}, logger: logger}

	for i := 0; i < promptCacheMissesBeforeWarn+5; i++ {
		client.notePromptCacheOutcome(true, "claude-sonnet-5", &Usage{PromptTokens: 5000})
	}

	if logs.Len() != 0 {
		t.Fatalf("a native Claude client warned about a gateway:\n%s", logs.String())
	}
}
