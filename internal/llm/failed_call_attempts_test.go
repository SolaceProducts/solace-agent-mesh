// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func streamCutBeforeItsFinish(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	chunk, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-cut", "object": "chat.completion.chunk", "created": 1234567890, "model": "test-model",
		"choices": []map[string]any{{"index": 0, "finish_reason": nil, "delta": map[string]any{"content": "partial"}}},
	})
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

func TestAStreamThatFailsAfterARetryReportsItsAttemptsAndModelOnEveryChunk(t *testing.T) {
	handler, _ := flakyHandler(1, streamCutBeforeItsFinish)
	server := mockOpenAIServer(t, handler)
	defer server.Close()
	client := newRetryTestClient(t, server.URL, new(1))

	stream, err := client.ChatCompletionStream(t.Context(), &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err != nil {
		t.Fatalf("expected the stream to open after a retry, got %v", err)
	}
	var last StreamChunk
	for chunk := range stream {
		if chunk.Attempts != 2 || chunk.Model != "openai/test-model" {
			t.Errorf("chunk %+v reports %d attempts to model %q, want 2 to openai/test-model", chunk, chunk.Attempts, chunk.Model)
		}
		last = chunk
	}
	if llmErr, ok := errors.AsType[*Error](last.Err); !ok || !llmErr.IsStreamTruncated() {
		t.Fatalf("last chunk = %+v, want a truncation error", last)
	}
}

func TestAStreamErrorCarryingBilledUsageReportsTheAttemptsAndModel(t *testing.T) {
	stream := make(chan *schemas.BifrostStreamChunk, 2)
	stream <- bifrostContentChunk("partial", "")
	stream <- &schemas.BifrostStreamChunk{BifrostError: &schemas.BifrostError{
		StatusCode:  schemas.Ptr(502),
		Error:       &schemas.ErrorField{Message: "upstream reset"},
		ExtraFields: schemas.BifrostErrorExtraFields{BilledUsage: &schemas.BifrostLLMUsage{PromptTokens: 900, CompletionTokens: 40, TotalTokens: 940}},
	}}
	close(stream)
	out := make(chan StreamChunk, 3)

	(&BifrostClient{cfg: BifrostConfig{Provider: "anthropic"}}).forwardStream(t.Context(), nil, stream, out, sentCall{model: "the-model", attempts: 3}, nil)
	close(out)

	var last StreamChunk
	for sc := range out {
		last = sc
	}
	if last.Err == nil || last.Attempts != 3 || last.Model != "the-model" {
		t.Fatalf("error chunk = %+v, want it to report 3 attempts to the-model", last)
	}
}

func TestACallRedirectedToTheResponsesAPICountsTheTurnedAwayAttemptWhenItFails(t *testing.T) {
	const responsesAttempts = 2
	newClient := func(t *testing.T) *BifrostClient {
		return newFakeOpenAIClientWithConfig(t, &fakeOpenAI{responsesStatus: http.StatusBadGateway},
			BifrostConfig{Model: "gpt-5-pro", MaxRetries: new(responsesAttempts - 1)})
	}
	hi := &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "Hi"}}}
	assertAttempts := func(t *testing.T, err error) {
		t.Helper()
		llmErr, ok := errors.AsType[*Error](err)
		if !ok {
			t.Fatalf("error = %v, want an *llm.Error", err)
		}
		if llmErr.Attempts != 1+responsesAttempts {
			t.Errorf("Attempts = %d, want %d: the chat attempt plus every Responses attempt", llmErr.Attempts, 1+responsesAttempts)
		}
	}

	t.Run("unary", func(t *testing.T) {
		_, err := newClient(t).ChatCompletion(t.Context(), hi)
		assertAttempts(t, err)
	})
	t.Run("stream", func(t *testing.T) {
		_, err := newClient(t).ChatCompletionStream(t.Context(), hi)
		assertAttempts(t, err)
	})
}
