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
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

type sseFrame map[string]any

func contentFrame(content, finishReason string) sseFrame {
	return sseFrame{
		"id":      "chatcmpl-stream",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": nilOrStr(finishReason),
			"delta":         map[string]any{"content": content},
		}},
	}
}

func usageOnlyFrame() sseFrame {
	return sseFrame{
		"id":      "chatcmpl-stream",
		"object":  "chat.completion.chunk",
		"created": 1234567890,
		"model":   "test-model",
		"choices": []map[string]any{},
		"usage": map[string]any{
			"prompt_tokens":     39601,
			"completion_tokens": 1,
			"total_tokens":      39602,
		},
	}
}

func sseFramesHandler(frames ...sseFrame) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		for _, f := range frames {
			b, _ := json.Marshal(f)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func drainStream(t *testing.T, handler http.HandlerFunc) []StreamChunk {
	t.Helper()
	server := mockOpenAIServer(t, handler)
	t.Cleanup(server.Close)

	stream, err := newTestClient(t, server.URL).ChatCompletionStream(t.Context(), &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var chunks []StreamChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func TestChatCompletionStream_NoFinishReasonIsTruncationError(t *testing.T) {
	chunks := drainStream(t, sseFramesHandler(contentFrame("«", ""), usageOnlyFrame()))

	if len(chunks) == 0 {
		t.Fatal("stream delivered no chunks")
	}
	lastIdx := len(chunks) - 1
	last := chunks[lastIdx]
	if last.Err == nil {
		t.Fatalf("last chunk = %+v, want a truncation error for a stream that ended without a finish reason", last)
	}
	llmErr, ok := errors.AsType[*Error](last.Err)
	if !ok {
		t.Fatalf("err = %T %v, want *llm.Error", last.Err, last.Err)
	}
	if llmErr.Type != streamTruncatedErrorType || !llmErr.IsStreamTruncated() {
		t.Errorf("err type = %q, want %q", llmErr.Type, streamTruncatedErrorType)
	}
	contentIdx := slices.IndexFunc(chunks, func(c StreamChunk) bool { return c.ContentDelta == "«" })
	if contentIdx < 0 || contentIdx >= lastIdx {
		t.Errorf("content delta index = %d, want it delivered before the error at %d", contentIdx, lastIdx)
	}
	usageIdx := slices.IndexFunc(chunks, func(c StreamChunk) bool { return c.Usage != nil && c.Usage.PromptTokens == 39601 })
	if usageIdx < 0 || usageIdx >= lastIdx {
		t.Errorf("usage chunk index = %d, want it forwarded before the error at %d", usageIdx, lastIdx)
	}
	if llmErr.StatusCode != 0 {
		t.Errorf("err status = %d, want 0: the provider returned 200, so no upstream status exists", llmErr.StatusCode)
	}
	for _, c := range chunks {
		if c.FinishReason != "" {
			t.Errorf("chunk carried finish reason %q on a stream that never sent one", c.FinishReason)
		}
	}
}

func bifrostContentChunk(content, finishReason string) *schemas.BifrostStreamChunk {
	choice := schemas.BifrostResponseChoice{
		ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
			Delta: &schemas.ChatStreamResponseChoiceDelta{Content: &content},
		},
	}
	if finishReason != "" {
		choice.FinishReason = &finishReason
	}
	return &schemas.BifrostStreamChunk{BifrostChatResponse: &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{choice},
	}}
}

func bifrostUsageChunk() *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{BifrostChatResponse: &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{{}},
		Usage:   &schemas.BifrostLLMUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
	}}
}

func forwardAll(t *testing.T, chunks ...*schemas.BifrostStreamChunk) []StreamChunk {
	t.Helper()
	stream := make(chan *schemas.BifrostStreamChunk, len(chunks))
	for _, c := range chunks {
		stream <- c
	}
	close(stream)

	out := make(chan StreamChunk, len(chunks)+1)
	client := &BifrostClient{cfg: BifrostConfig{Provider: "anthropic"}}
	client.forwardStream(t.Context(), nil, stream, out, sentCall{attempts: 1}, nil)
	close(out)

	var got []StreamChunk
	for sc := range out {
		got = append(got, sc)
	}
	return got
}

func finishReasons(chunks []StreamChunk) []string {
	var reasons []string
	for _, c := range chunks {
		if c.FinishReason != "" {
			reasons = append(reasons, c.FinishReason)
		}
	}
	return reasons
}

func TestForwardStream_HeldFinishReasonEmittedWhenNoUsageChunkFollows(t *testing.T) {
	got := forwardAll(t, bifrostContentChunk("Hello", ""), bifrostContentChunk("!", "stop"))

	if reasons := finishReasons(got); len(reasons) != 1 || reasons[0] != "stop" {
		t.Fatalf("finish reasons = %v, want exactly [stop]", reasons)
	}
	last := got[len(got)-1]
	if last.FinishReason != "stop" || last.Err != nil {
		t.Errorf("last chunk = %+v, want finish reason stop and no error", last)
	}
}

func TestForwardStream_FinishReasonReattachedToUsageChunkOnly(t *testing.T) {
	got := forwardAll(t, bifrostContentChunk("Hi", "stop"), bifrostUsageChunk())

	if len(got) != 2 {
		t.Fatalf("got %d chunks, want 2 (content + usage), no synthesized trailer", len(got))
	}
	if got[0].FinishReason != "" {
		t.Errorf("content chunk finish reason = %q, want it held back", got[0].FinishReason)
	}
	if got[1].FinishReason != "stop" || got[1].Usage == nil {
		t.Errorf("usage chunk = %+v, want finish reason stop with usage", got[1])
	}
}

func TestForwardStream_NoFinishReasonEndsWithTruncationError(t *testing.T) {
	got := forwardAll(t, bifrostContentChunk("«", ""), bifrostUsageChunk())

	if len(got) != 3 {
		t.Fatalf("got %d chunks %+v, want 3 (content, usage, truncation error)", len(got), got)
	}
	if reasons := finishReasons(got); len(reasons) != 0 {
		t.Errorf("finish reasons = %v, want none", reasons)
	}
	if got[0].ContentDelta != "«" || got[0].Err != nil {
		t.Errorf("chunk 0 = %+v, want content delta «", got[0])
	}
	if got[1].Usage == nil || got[1].Err != nil {
		t.Errorf("chunk 1 = %+v, want the usage chunk forwarded without an error", got[1])
	}
	llmErr, ok := errors.AsType[*Error](got[2].Err)
	if !ok {
		t.Fatalf("chunk 2 = %+v, want a *llm.Error truncation error", got[2])
	}
	if !llmErr.IsStreamTruncated() || llmErr.Type != streamTruncatedErrorType || llmErr.Provider != "anthropic" || llmErr.StatusCode != 0 {
		t.Errorf("err = %+v, want %s error from provider anthropic with no status code", llmErr, streamTruncatedErrorType)
	}
}

func bifrostToolCallChunk(id, name, argsFragment string) *schemas.BifrostStreamChunk {
	return &schemas.BifrostStreamChunk{BifrostChatResponse: &schemas.BifrostChatResponse{
		Choices: []schemas.BifrostResponseChoice{{
			ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
				Delta: &schemas.ChatStreamResponseChoiceDelta{
					ToolCalls: []schemas.ChatAssistantMessageToolCall{{
						ID:       &id,
						Function: schemas.ChatAssistantMessageToolCallFunction{Name: &name, Arguments: argsFragment},
					}},
				},
			},
		}},
	}}
}

func TestForwardStream_ToolCallCutWithoutFinishReasonEndsWithTruncationError(t *testing.T) {
	got := forwardAll(t, bifrostToolCallChunk("call_1", "save_artifact", `{"filename":"rep`))

	if len(got) != 2 {
		t.Fatalf("got %d chunks %+v, want 2 (tool-call delta, truncation error)", len(got), got)
	}
	deltas := got[0].ToolCallDeltas
	if len(deltas) != 1 || deltas[0].ID != "call_1" || deltas[0].Name != "save_artifact" || deltas[0].ArgumentsDelta != `{"filename":"rep` {
		t.Errorf("chunk 0 tool-call deltas = %+v, want the partial save_artifact call forwarded", deltas)
	}
	if reasons := finishReasons(got); len(reasons) != 0 {
		t.Errorf("finish reasons = %v, want none: a finish reason would let the partial call execute", reasons)
	}
	llmErr, ok := errors.AsType[*Error](got[1].Err)
	if !ok || !llmErr.IsStreamTruncated() {
		t.Errorf("chunk 1 = %+v, want a truncation error", got[1])
	}
}

func TestForwardStream_BifrostErrorAfterFinishReasonEndsWithThatError(t *testing.T) {
	status := http.StatusBadGateway
	bfErr := &schemas.BifrostStreamChunk{BifrostError: &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "upstream reset"},
	}}
	got := forwardAll(t, bifrostContentChunk("Hi", "stop"), bfErr)

	if len(got) != 2 {
		t.Fatalf("got %d chunks %+v, want 2 (content, bifrost error)", len(got), got)
	}
	if got[0].ContentDelta != "Hi" || got[0].FinishReason != "" || got[0].Err != nil {
		t.Errorf("chunk 0 = %+v, want content Hi with its finish reason held back", got[0])
	}
	llmErr, ok := errors.AsType[*Error](got[1].Err)
	if !ok {
		t.Fatalf("chunk 1 = %+v, want a *llm.Error", got[1])
	}
	if llmErr.IsStreamTruncated() || llmErr.Message != "upstream reset" || llmErr.StatusCode != status {
		t.Errorf("err = %+v, want the Bifrost error, not a truncation error", llmErr)
	}
	if reasons := finishReasons(got); len(reasons) != 0 {
		t.Errorf("finish reasons = %v, want none on a stream that ended in an error", reasons)
	}
}

func TestStreamTruncatedError_Rendering(t *testing.T) {
	err := (&BifrostClient{cfg: BifrostConfig{Provider: "openai"}}).endOfStreamChunk("").Err

	if got := UserFacingErrorMessage(err); got != StreamTruncatedUserMessage {
		t.Errorf("UserFacingErrorMessage = %q, want %q", got, StreamTruncatedUserMessage)
	}
	if got := SanitizeErrorText(err); got != StreamTruncatedUserMessage {
		t.Errorf("SanitizeErrorText = %q, want %q", got, StreamTruncatedUserMessage)
	}
	for _, want := range []string{"stream_truncated", "stream ended without a finish reason"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Error() = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestForwardStream_CancelledStreamEndsWithoutTruncationError(t *testing.T) {
	for range 200 {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		stream := make(chan *schemas.BifrostStreamChunk)
		close(stream)
		out := make(chan StreamChunk, 1)

		(&BifrostClient{}).forwardStream(ctx, nil, stream, out, sentCall{attempts: 1}, nil)
		close(out)

		for sc := range out {
			t.Fatalf("cancelled stream emitted %+v, want nothing", sc)
		}
	}
}

func TestChatCompletionStream_FinishReasonOnContentChunkDeliveredOnce(t *testing.T) {
	chunks := drainStream(t, sseFramesHandler(contentFrame("Hello", ""), contentFrame("!", "stop")))

	var terminal int
	for _, c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected stream error: %v", c.Err)
		}
		if c.FinishReason != "" {
			terminal++
			if c.FinishReason != "stop" {
				t.Errorf("finish reason = %q, want stop", c.FinishReason)
			}
		}
	}
	if terminal != 1 {
		t.Errorf("got %d chunks carrying a finish reason, want exactly 1", terminal)
	}
	if last := chunks[len(chunks)-1]; last.FinishReason != "stop" {
		t.Errorf("last chunk finish reason = %q, want stop", last.FinishReason)
	}
}
