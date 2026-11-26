// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func anthropicMessage(usage map[string]any) func(<-chan struct{}) http.HandlerFunc {
	return func(<-chan struct{}) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant", "model": billedUsageModel,
				"content":     []any{map[string]any{"type": "text", "text": "reply"}},
				"stop_reason": "end_turn",
				"usage":       usage,
			})
		}
	}
}

func anthropicStream(usage map[string]any) func(<-chan struct{}) http.HandlerFunc {
	return func(<-chan struct{}) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			writeAnthropicEvent(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant", "model": billedUsageModel, "content": []any{},
				"usage": usage,
			}})
			writeAnthropicEvent(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "text", "text": ""}})
			writeAnthropicEvent(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]any{"type": "text_delta", "text": "reply"}})
			writeAnthropicEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			writeAnthropicEvent(w, "message_delta", map[string]any{"type": "message_delta",
				"delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 3}})
			writeAnthropicEvent(w, "message_stop", map[string]any{"type": "message_stop"})
		}
	}
}

func lastStreamUsage(t *testing.T, client *BifrostClient) *Usage {
	t.Helper()
	stream, err := client.ChatCompletionStream(t.Context(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var usage *Usage
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if usage == nil {
		t.Fatal("stream ended without usage")
	}
	return usage
}

func TestAnthropicUsage_ReportsItsCacheWritesEvenWhenThereAreNone(t *testing.T) {
	cacheReadOnly := map[string]any{"input_tokens": 10, "cache_read_input_tokens": 50, "cache_creation_input_tokens": 0, "output_tokens": 3}

	t.Run("non-streaming", func(t *testing.T) {
		client := newAnthropicTestClient(t, anthropicMessage(cacheReadOnly))
		resp, err := client.ChatCompletion(t.Context(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Usage == nil || !resp.Usage.CacheWritesReported || resp.Usage.CacheWriteTokens != 0 || resp.Usage.CachedTokens != 50 {
			t.Fatalf("usage = %+v, want 50 cached tokens and a reported write count of 0", resp.Usage)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		usage := lastStreamUsage(t, newAnthropicTestClient(t, anthropicStream(cacheReadOnly)))
		if !usage.CacheWritesReported || usage.CacheWriteTokens != 0 || usage.CachedTokens != 50 {
			t.Fatalf("usage = %+v, want 50 cached tokens and a reported write count of 0", usage)
		}
	})
}

func TestOpenAIUsage_LeavesItsCacheWritesUnreported(t *testing.T) {
	server := mockOpenAIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1234567890, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "reply"},
			}},
			"usage": map[string]any{
				"prompt_tokens": 100, "completion_tokens": 3, "total_tokens": 103,
				"prompt_tokens_details": map[string]any{"cached_tokens": 50},
			},
		})
	})
	defer server.Close()

	resp, err := newTestClient(t, server.URL).ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil || resp.Usage.CacheWritesReported || resp.Usage.CachedTokens != 50 {
		t.Fatalf("usage = %+v, want 50 cached tokens and no reported write count", resp.Usage)
	}
}

func TestOpenAIUsage_ReportsTheCacheWritesAGatewayForwarded(t *testing.T) {
	server := mockOpenAIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1234567890, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "reply"},
			}},
			"usage": map[string]any{
				"prompt_tokens": 100, "completion_tokens": 3, "total_tokens": 103,
				"prompt_tokens_details": map[string]any{"cached_read_tokens": 50, "cached_write_tokens": 20},
			},
		})
	})
	defer server.Close()

	resp, err := newTestClient(t, server.URL).ChatCompletion(context.Background(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage == nil || !resp.Usage.CacheWritesReported || resp.Usage.CacheWriteTokens != 20 {
		t.Fatalf("usage = %+v, want a reported write count of 20", resp.Usage)
	}
}
