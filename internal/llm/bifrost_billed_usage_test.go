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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

const billedUsageModel = "claude-sonnet-4-5"

func writeAnthropicEvent(w http.ResponseWriter, event string, data any) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	w.(http.Flusher).Flush()
}

func anthropicStreamThatStalls(release <-chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeAnthropicEvent(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": billedUsageModel, "content": []any{},
			"usage": map[string]any{"input_tokens": 1000, "cache_read_input_tokens": 4000, "output_tokens": 1},
		}})
		writeAnthropicEvent(w, "content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}})
		writeAnthropicEvent(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "hello"}})
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}
}

func newAnthropicTestClient(t *testing.T, newHandler func(release <-chan struct{}) http.HandlerFunc) *BifrostClient {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(newHandler(release))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := NewBifrostClient(ctx, BifrostConfig{Provider: "anthropic", Model: billedUsageModel, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func TestChatCompletionStream_AStreamEndedByItsContextHandsOffTheUsageTheProviderBilled(t *testing.T) {
	client := newAnthropicTestClient(t, anthropicStreamThatStalls)
	ends := map[string]func() (context.Context, context.CancelFunc){
		"cancelled": func() (context.Context, context.CancelFunc) { return context.WithCancel(t.Context()) },
		"timed out": func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(t.Context(), 300*time.Millisecond)
		},
	}
	for name, newCtx := range ends {
		t.Run(name, func(t *testing.T) {
			for run := range 10 {
				ctx, cancel := newCtx()
				stream, err := client.ChatCompletionStream(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				for chunk := range stream {
					if chunk.ContentDelta != "" {
						break
					}
				}
				if name == "cancelled" {
					cancel()
				}
				var billed *Usage
				for chunk := range stream {
					if llmErr, ok := errors.AsType[*Error](chunk.Err); ok && llmErr.BilledUsage != nil {
						billed = llmErr.BilledUsage
						if chunk.Attempts != 1 || chunk.Model != "anthropic/"+billedUsageModel {
							t.Fatalf("run %d: billed usage chunk reports %d attempts to %q, want 1 to anthropic/%s", run, chunk.Attempts, chunk.Model, billedUsageModel)
						}
					}
				}
				cancel()
				if billed == nil {
					t.Fatalf("run %d: stream closed without the billed usage", run)
				}
				if billed.PromptTokens != 5000 || billed.CachedTokens != 4000 || billed.CompletionTokens != 1 {
					t.Fatalf("run %d: billed usage = %+v, want 5000 prompt tokens of which 4000 cached, 1 completion token", run, *billed)
				}
			}
		})
	}
}

func TestBifrostErrorToError_CarriesTheBilledUsage(t *testing.T) {
	bfErr := &schemas.BifrostError{
		StatusCode: schemas.Ptr(499),
		Error:      &schemas.ErrorField{Message: "Request cancelled"},
		ExtraFields: schemas.BifrostErrorExtraFields{BilledUsage: &schemas.BifrostLLMUsage{
			PromptTokens:     7000,
			CompletionTokens: 30,
			TotalTokens:      7030,
			PromptTokensDetails: &schemas.ChatPromptTokensDetails{
				CachedReadTokens:  4000,
				CachedWriteTokens: 2000,
			},
		}},
	}

	got := bifrostErrorToError(bfErr, "").BilledUsage

	want := Usage{PromptTokens: 7000, CompletionTokens: 30, TotalTokens: 7030, CachedTokens: 6000, CacheWriteTokens: 2000, CacheWritesReported: true}
	if got == nil || *got != want {
		t.Fatalf("billed usage = %+v, want %+v", got, want)
	}
}

func TestForwardStream_AMidStreamErrorKeepsTheUsageItBilled(t *testing.T) {
	billed := &schemas.BifrostLLMUsage{PromptTokens: 900, CompletionTokens: 40, TotalTokens: 940}
	chunks := forwardAll(t,
		bifrostContentChunk("partial", ""),
		&schemas.BifrostStreamChunk{BifrostError: &schemas.BifrostError{
			StatusCode:  schemas.Ptr(502),
			Error:       &schemas.ErrorField{Message: "upstream reset"},
			ExtraFields: schemas.BifrostErrorExtraFields{BilledUsage: billed},
		}},
	)

	last := chunks[len(chunks)-1]
	llmErr, ok := errors.AsType[*Error](last.Err)
	if !ok || llmErr.BilledUsage == nil || llmErr.BilledUsage.PromptTokens != 900 || llmErr.BilledUsage.CompletionTokens != 40 {
		t.Fatalf("last chunk = %+v, want an error carrying 900 prompt and 40 completion billed tokens", last)
	}
}

func TestForwardStream_ATruncatedStreamReportsTheUsageTheProviderAccumulated(t *testing.T) {
	bfCtx := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	bfCtx.SetValue(schemas.BifrostContextKeyStreamAccumulatedUsage,
		&schemas.BifrostLLMUsage{PromptTokens: 1200, CompletionTokens: 60, TotalTokens: 1260})
	stream := make(chan *schemas.BifrostStreamChunk, 1)
	stream <- bifrostContentChunk("partial", "")
	close(stream)
	out := make(chan StreamChunk, 4)

	(&BifrostClient{cfg: BifrostConfig{Provider: "anthropic"}}).forwardStream(t.Context(), bfCtx, stream, out, sentCall{attempts: 1}, nil)
	close(out)

	var last StreamChunk
	for sc := range out {
		last = sc
	}
	llmErr, ok := errors.AsType[*Error](last.Err)
	if !ok || !llmErr.IsStreamTruncated() {
		t.Fatalf("last chunk = %+v, want a truncation error", last)
	}
	if llmErr.BilledUsage == nil || llmErr.BilledUsage.PromptTokens != 1200 || llmErr.BilledUsage.CompletionTokens != 60 {
		t.Fatalf("billed usage = %+v, want 1200 prompt and 60 completion tokens", llmErr.BilledUsage)
	}
}
