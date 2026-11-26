// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const anthropicWireModel = "claude-sonnet-4-5"

var anthropicWireUsage = map[string]any{
	"input_tokens":                1000,
	"cache_read_input_tokens":     4000,
	"cache_creation_input_tokens": 2000,
	"output_tokens":               500,
}

var anthropicWireTokens = Tokens{UncachedInput: 1000, CacheRead: 4000, CacheWrite: 2000, Output: 500}

func anthropicClient(t *testing.T, handler http.HandlerFunc) *llm.BifrostClient {
	t.Helper()
	return wireClient(t, "anthropic", anthropicWireModel, handler)
}

func wireClient(t *testing.T, provider, model string, handler http.HandlerFunc) *llm.BifrostClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv(strings.ToUpper(provider)+"_API_KEY", "test-key")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := llm.NewBifrostClient(ctx, llm.BifrostConfig{
		Provider: provider,
		Model:    model,
		BaseURL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func writeSSE(w http.ResponseWriter, event string, data any) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	w.(http.Flusher).Flush()
}

func TestRecordingClient_ANonStreamingAnthropicResponseRecordsTheCacheExclusiveInputAsUncachedInput(t *testing.T) {
	inner := anthropicClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": anthropicWireModel,
			"content":     []map[string]any{{"type": "text", "text": "hello"}},
			"stop_reason": "end_turn",
			"usage":       anthropicWireUsage,
		})
	})
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	client := NewRecordingClient(inner, anthropicWireModel, pub)

	_, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
		Spend:    attribution(),
	})
	if err != nil {
		t.Fatalf("chat completion: %v", err)
	}

	events := bus.published(t, pub)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Tokens != anthropicWireTokens {
		t.Errorf("tokens = %+v, want %+v", events[0].Tokens, anthropicWireTokens)
	}
}

func TestRecordingClient_AStreamingAnthropicResponseRecordsTheCacheExclusiveInputAsUncachedInput(t *testing.T) {
	inner := anthropicClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		startUsage := map[string]any{}
		for k, v := range anthropicWireUsage {
			startUsage[k] = v
		}
		startUsage["output_tokens"] = 1
		writeSSE(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": anthropicWireModel,
			"content": []any{}, "usage": startUsage,
		}})
		writeSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}})
		writeSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "hello"}})
		writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		writeSSE(w, "message_delta", map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"output_tokens": 500}})
		writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
	})
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	client := NewRecordingClient(inner, anthropicWireModel, pub)

	stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
		Spend:    attribution(),
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream {
	}

	events := bus.published(t, pub)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Tokens != anthropicWireTokens {
		t.Errorf("tokens = %+v, want %+v", events[0].Tokens, anthropicWireTokens)
	}
}

func TestRecordingClient_AStreamingOpenAIResponseRecordsTheUsageOnItsTrailingChunk(t *testing.T) {
	const model = "gpt-4o"
	inner := wireClient(t, "openai", model, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeData := func(data any) {
			payload, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			w.(http.Flusher).Flush()
		}
		chunk := func(choices []map[string]any, usage any) map[string]any {
			return map[string]any{"id": "c1", "object": "chat.completion.chunk", "created": 1, "model": model,
				"choices": choices, "usage": usage}
		}
		writeData(chunk([]map[string]any{{"index": 0, "delta": map[string]any{"content": "hello"}, "finish_reason": nil}}, nil))
		writeData(chunk([]map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil))
		writeData(chunk([]map[string]any{}, map[string]any{
			"prompt_tokens": 1000, "completion_tokens": 50, "total_tokens": 1050,
			"prompt_tokens_details": map[string]any{"cached_tokens": 600},
		}))
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	})
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	client := NewRecordingClient(inner, model, pub)

	stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
		Spend:    attribution(),
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream {
	}

	events := bus.published(t, pub)
	if want := (Tokens{UncachedInput: 400, CacheRead: 600, Output: 50}); len(events) != 1 || events[0].Tokens != want {
		t.Errorf("events = %+v, want one with tokens %+v", events, want)
	}
}

func TestRecordingClient_ACancelledAnthropicStreamIsRecordedFromTheUsageTheProviderBilled(t *testing.T) {
	release := make(chan struct{})
	inner := anthropicClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		startUsage := map[string]any{}
		for k, v := range anthropicWireUsage {
			startUsage[k] = v
		}
		startUsage["output_tokens"] = 1
		writeSSE(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": anthropicWireModel,
			"content": []any{}, "usage": startUsage,
		}})
		writeSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}})
		writeSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "hello"}})
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	want := anthropicWireTokens
	want.Output = 1

	for run := range 5 {
		bus := newSpendBus(t)
		pub := bus.publisher(t)
		client := NewRecordingClient(inner, anthropicWireModel, pub)
		ctx, cancel := context.WithCancel(t.Context())
		stream, err := client.ChatCompletionStream(ctx, &llm.ChatRequest{
			Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
			Spend:    attribution(),
		})
		if err != nil {
			cancel()
			t.Fatalf("stream: %v", err)
		}
		for chunk := range stream {
			if chunk.ContentDelta != "" {
				break
			}
		}
		cancel()
		for range stream {
		}

		events := bus.published(t, pub)
		if len(events) != 1 || events[0].Tokens != want {
			t.Fatalf("run %d: events = %+v, want one with tokens %+v", run, events, want)
		}
	}
}
