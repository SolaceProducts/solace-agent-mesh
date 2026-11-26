// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const usageScenarioJSON = `{
	"responses": [{
		"choices": [{"message": {"role": "assistant", "content": "hi"}}],
		"usage": {"prompt_tokens": 1000, "completion_tokens": 50, "total_tokens": 1050,
		          "cached_tokens": 600, "cache_write_tokens": 200, "reasoning_tokens": 30}
	}]
}`

func wantScenarioUsage(t *testing.T, got *llm.Usage) {
	t.Helper()
	if got == nil {
		t.Fatal("usage missing")
	}
	want := llm.Usage{
		PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050,
		CachedTokens: 600, CacheWriteTokens: 200, ReasoningTokens: 30,
	}
	got.CacheWritesReported = false
	if *got != want {
		t.Errorf("usage = %+v, want %+v", *got, want)
	}
}

func bifrostAgainstUsageScenario(t *testing.T) (context.Context, llm.Client) {
	t.Helper()
	sc, err := ParseScenario([]byte(usageScenarioJSON))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	srv := NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.LoadScenarios(sc)
	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("start server: %v", startErr)
	}
	t.Cleanup(srv.Stop)

	t.Setenv("OPENAI_API_KEY", "test-key")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	client, err := llm.NewBifrostClient(ctx, llm.BifrostConfig{
		Provider: "openai",
		Model:    "perf-test-model",
		BaseURL:  "http://" + srv.Addr(),
	})
	if err != nil {
		t.Fatalf("new bifrost client: %v", err)
	}
	t.Cleanup(client.Shutdown)
	return ctx, client
}

func TestHTTPUsageReachesBifrostWithCacheAndReasoningSplit(t *testing.T) {
	t.Run("non-streaming", func(t *testing.T) {
		ctx, client := bifrostAgainstUsageScenario(t)
		resp, err := client.ChatCompletion(ctx, &llm.ChatRequest{
			Messages: []llm.ChatMessage{{Role: "user", Content: "hello"}},
		})
		if err != nil {
			t.Fatalf("completion: %v", err)
		}
		wantScenarioUsage(t, resp.Usage)
	})

	t.Run("streaming", func(t *testing.T) {
		ctx, client := bifrostAgainstUsageScenario(t)
		stream, err := client.ChatCompletionStream(ctx, &llm.ChatRequest{
			Messages: []llm.ChatMessage{{Role: "user", Content: "hello"}},
		})
		if err != nil {
			t.Fatalf("start stream: %v", err)
		}
		var usage *llm.Usage
		for chunk := range stream {
			if chunk.Err != nil {
				t.Fatalf("stream: %v", chunk.Err)
			}
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
		}
		wantScenarioUsage(t, usage)
	})
}

func TestAScenarioWritingMoreToTheCacheThanItReadsIsRejected(t *testing.T) {
	_, err := ParseScenario([]byte(`{"responses": [{
		"choices": [{"message": {"role": "assistant", "content": "hi"}}],
		"usage": {"prompt_tokens": 100, "completion_tokens": 5, "cached_tokens": 0, "cache_write_tokens": 40}
	}]}`))
	if err == nil {
		t.Fatal("a usage with cache_write_tokens above cached_tokens parsed, want it rejected")
	}
}

func TestInProcessClientReadsUsageFromPrecomputedWireJSON(t *testing.T) {
	var sc struct {
		Responses []json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal([]byte(usageScenarioJSON), &sc); err != nil {
		t.Fatal(err)
	}
	var resp ChatCompletionResponse
	if err := json.Unmarshal(sc.Responses[0], &resp); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ConvertOpenAIResponse(wire)
	if err != nil {
		t.Fatal(err)
	}
	wantScenarioUsage(t, got.Usage)
}

func TestUsageMarshalRejectsCacheWritesBeyondCachedTokens(t *testing.T) {
	if _, err := json.Marshal(Usage{CachedTokens: 10, CacheWriteTokens: 20}); err == nil {
		t.Fatal("marshal accepted cache writes exceeding cached tokens")
	}
}
