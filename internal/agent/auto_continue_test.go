// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func makeStream(chunks ...llm.StreamChunk) <-chan llm.StreamChunk {
	ch := make(chan llm.StreamChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch
}

func collectStream(ch <-chan llm.StreamChunk) []llm.StreamChunk {
	var result []llm.StreamChunk
	for c := range ch {
		result = append(result, c)
	}
	return result
}

func noopInvoker(t *testing.T) func(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		t.Fatal("invoke should not be called")
		return nil, nil
	}
}

func TestAutoContinueStream_NormalFinish(t *testing.T) {
	stream := makeStream(
		llm.StreamChunk{ContentDelta: "Hello "},
		llm.StreamChunk{ContentDelta: "world", FinishReason: "stop", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5}},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(context.Background(), stream, req, noopInvoker(t), slog.Default(), -1)
	chunks := collectStream(out)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ContentDelta != "Hello " {
		t.Errorf("chunk 0: expected 'Hello ', got %q", chunks[0].ContentDelta)
	}
	if chunks[1].FinishReason != "stop" {
		t.Errorf("expected finish_reason 'stop', got %q", chunks[1].FinishReason)
	}
	if chunks[1].Usage == nil || chunks[1].Usage.PromptTokens != 10 {
		t.Errorf("expected usage with 10 prompt tokens, got %+v", chunks[1].Usage)
	}
}

func TestAutoContinueStream_MaxTokensTextOnly(t *testing.T) {
	stream1 := makeStream(
		llm.StreamChunk{ContentDelta: "part one "},
		llm.StreamChunk{ContentDelta: "continues", FinishReason: "max_tokens", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 20}},
	)

	var invokedReq *llm.ChatRequest
	invokeCount := 0
	invoke := func(_ context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokeCount++
		invokedReq = req
		return makeStream(
			llm.StreamChunk{ContentDelta: " part two"},
			llm.StreamChunk{FinishReason: "stop", Usage: &llm.Usage{PromptTokens: 50, CompletionTokens: 10}},
		), nil
	}

	req := &llm.ChatRequest{
		Model:    "test",
		Messages: []llm.ChatMessage{{Role: "user", Content: "write a story"}},
		Tools:    []llm.ToolDefinition{{Name: "_continue_generation"}},
	}
	out, _ := autoContinueStream(context.Background(), stream1, req, invoke, slog.Default(), -1)
	chunks := collectStream(out)

	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d: %+v", len(chunks), chunks)
	}

	if chunks[0].ContentDelta != "part one " {
		t.Errorf("chunk 0: %q", chunks[0].ContentDelta)
	}
	if chunks[1].ContentDelta != "continues" {
		t.Errorf("chunk 1: %q", chunks[1].ContentDelta)
	}
	if chunks[1].FinishReason != "" {
		t.Errorf("chunk 1 should not have finish_reason, got %q", chunks[1].FinishReason)
	}

	if chunks[2].ContentDelta != " part two" {
		t.Errorf("chunk 2: %q", chunks[2].ContentDelta)
	}

	if chunks[3].FinishReason != "stop" {
		t.Errorf("final finish_reason: %q", chunks[3].FinishReason)
	}
	if chunks[3].Usage == nil {
		t.Fatal("final chunk should have usage")
	}
	if chunks[3].Usage.PromptTokens != 60 {
		t.Errorf("total prompt tokens: expected 60, got %d", chunks[3].Usage.PromptTokens)
	}
	if chunks[3].Usage.CompletionTokens != 30 {
		t.Errorf("total completion tokens: expected 30, got %d", chunks[3].Usage.CompletionTokens)
	}

	if invokeCount != 1 {
		t.Errorf("expected 1 invoke, got %d", invokeCount)
	}

	if invokedReq == nil {
		t.Fatal("invoke was not called")
	}
	msgs := invokedReq.Messages
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("msg[1] role: %q", msgs[1].Role)
	}
	if msgs[1].Content != "part one continues" {
		t.Errorf("msg[1] content: %q", msgs[1].Content)
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].Name != "_continue_generation" {
		t.Errorf("msg[1] tool calls: %+v", msgs[1].ToolCalls)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolName != "_continue_generation" {
		t.Errorf("msg[2]: role=%q, tool=%q", msgs[2].Role, msgs[2].ToolName)
	}
}

func TestAutoContinueStream_LengthFinishReason(t *testing.T) {
	stream1 := makeStream(
		llm.StreamChunk{ContentDelta: "text", FinishReason: "length"},
	)

	invoked := false
	invoke := func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invoked = true
		return makeStream(
			llm.StreamChunk{ContentDelta: " more", FinishReason: "stop"},
		), nil
	}

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(context.Background(), stream1, req, invoke, slog.Default(), -1)
	chunks := collectStream(out)

	if !invoked {
		t.Fatal("expected invoke for finish_reason='length'")
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ContentDelta != "text" {
		t.Errorf("chunk 0: %q", chunks[0].ContentDelta)
	}
	last := chunks[len(chunks)-1]
	if last.FinishReason != "stop" {
		t.Errorf("final finish_reason: %q", last.FinishReason)
	}
}

func TestAutoContinueStream_MaxTokensWithToolCalls(t *testing.T) {
	stream := makeStream(
		llm.StreamChunk{ContentDelta: "text"},
		llm.StreamChunk{ToolCallDeltas: []llm.ToolCallDelta{{Index: 0, ID: "call1", Name: "search"}}},
		llm.StreamChunk{FinishReason: "max_tokens"},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(context.Background(), stream, req, noopInvoker(t), slog.Default(), -1)
	chunks := collectStream(out)

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[2].FinishReason != "max_tokens" {
		t.Errorf("expected max_tokens pass-through, got %q", chunks[2].FinishReason)
	}
}

func TestAutoContinueStream_MaxContinuationsGuard(t *testing.T) {
	invokeCount := 0
	invoke := func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokeCount++
		return makeStream(
			llm.StreamChunk{ContentDelta: fmt.Sprintf("part%d", invokeCount), FinishReason: "max_tokens"},
		), nil
	}

	stream := makeStream(
		llm.StreamChunk{ContentDelta: "start", FinishReason: "max_tokens"},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(context.Background(), stream, req, invoke, slog.Default(), -1)
	chunks := collectStream(out)

	if invokeCount != maxAutoContinuations {
		t.Errorf("expected %d invocations, got %d", maxAutoContinuations, invokeCount)
	}

	lastChunk := chunks[len(chunks)-1]
	if lastChunk.FinishReason != "max_tokens" {
		t.Errorf("expected final max_tokens pass-through, got %q", lastChunk.FinishReason)
	}
}

func TestAutoContinueStream_MidArtifact(t *testing.T) {
	artifactOpener := "«««save_artifact: filename=\"chapter.md\" mime_type=\"text/markdown\"\n"
	stream1 := makeStream(
		llm.StreamChunk{ContentDelta: "Here is the chapter:\n"},
		llm.StreamChunk{ContentDelta: artifactOpener},
		llm.StreamChunk{ContentDelta: "# Chapter 1\n\nOnce upon a time"},
		llm.StreamChunk{FinishReason: "max_tokens"},
	)

	var invokedReq *llm.ChatRequest
	invoke := func(_ context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokedReq = req
		return makeStream(
			llm.StreamChunk{ContentDelta: " in a land far away...\n»»»\n"},
			llm.StreamChunk{FinishReason: "stop"},
		), nil
	}

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "write chapter"}}}
	out, _ := autoContinueStream(context.Background(), stream1, req, invoke, slog.Default(), -1)
	chunks := collectStream(out)

	var fullText strings.Builder
	for _, c := range chunks {
		fullText.WriteString(c.ContentDelta)
	}
	expected := "Here is the chapter:\n" + artifactOpener + "# Chapter 1\n\nOnce upon a time in a land far away...\n»»»\n"
	if fullText.String() != expected {
		t.Errorf("full text mismatch:\ngot:  %q\nwant: %q", fullText.String(), expected)
	}

	if invokedReq == nil {
		t.Fatal("invoke was not called")
	}
	assistantMsg := invokedReq.Messages[1]
	if !strings.Contains(assistantMsg.Content, "«««save_artifact:") {
		t.Error("continuation history should contain raw artifact opener")
	}
	if !strings.Contains(assistantMsg.Content, "# Chapter 1") {
		t.Error("continuation history should contain artifact content")
	}
}

func TestAutoContinueStream_InvokeError(t *testing.T) {
	stream := makeStream(
		llm.StreamChunk{ContentDelta: "text", FinishReason: "max_tokens"},
	)

	invoke := func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		return nil, fmt.Errorf("LLM unavailable")
	}

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(context.Background(), stream, req, invoke, slog.Default(), -1)
	chunks := collectStream(out)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ContentDelta != "text" {
		t.Errorf("chunk 0: %q", chunks[0].ContentDelta)
	}
	if chunks[1].Err == nil {
		t.Fatal("expected error chunk")
	}
	if !strings.Contains(chunks[1].Err.Error(), "LLM unavailable") {
		t.Errorf("error: %v", chunks[1].Err)
	}
}

func TestAutoContinueStream_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	blockingStream := make(chan llm.StreamChunk)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(ctx, blockingStream, req, noopInvoker(t), slog.Default(), -1)

	cancel()

	chunks := collectStream(out)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks after cancel, got %d", len(chunks))
	}
}

func TestAutoContinueStream_MultipleContinuations(t *testing.T) {
	invokeCount := 0
	invoke := func(_ context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokeCount++
		if invokeCount < 3 {
			return makeStream(
				llm.StreamChunk{ContentDelta: fmt.Sprintf(" part%d", invokeCount+1), FinishReason: "max_tokens"},
			), nil
		}
		return makeStream(
			llm.StreamChunk{ContentDelta: " end", FinishReason: "stop"},
		), nil
	}

	stream := makeStream(
		llm.StreamChunk{ContentDelta: "part1", FinishReason: "max_tokens"},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, _ := autoContinueStream(context.Background(), stream, req, invoke, slog.Default(), -1)
	chunks := collectStream(out)

	var fullText strings.Builder
	for _, c := range chunks {
		fullText.WriteString(c.ContentDelta)
	}
	if fullText.String() != "part1 part2 part3 end" {
		t.Errorf("full text: %q", fullText.String())
	}

	if invokeCount != 3 {
		t.Errorf("expected 3 invocations, got %d", invokeCount)
	}

	last := chunks[len(chunks)-1]
	if last.FinishReason != "stop" {
		t.Errorf("final finish_reason: %q", last.FinishReason)
	}
}

func TestAutoContinueStream_PreservesOriginalMessages(t *testing.T) {
	originalMessages := []llm.ChatMessage{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Write a story."},
	}

	stream := makeStream(
		llm.StreamChunk{ContentDelta: "once", FinishReason: "max_tokens"},
	)

	invoke := func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		return makeStream(
			llm.StreamChunk{ContentDelta: " upon", FinishReason: "stop"},
		), nil
	}

	req := &llm.ChatRequest{Model: "test", Messages: originalMessages}
	out, _ := autoContinueStream(context.Background(), stream, req, invoke, slog.Default(), -1)
	collectStream(out)

	if len(req.Messages) != 2 {
		t.Errorf("original messages mutated: len=%d", len(req.Messages))
	}
}

func TestAutoContinueStream_ContinuationCountReported(t *testing.T) {
	invokeCount := 0
	invoke := func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokeCount++
		if invokeCount < 3 {
			return makeStream(
				llm.StreamChunk{ContentDelta: fmt.Sprintf("part%d ", invokeCount), FinishReason: "max_tokens"},
			), nil
		}
		return makeStream(
			llm.StreamChunk{ContentDelta: "end", FinishReason: "stop"},
		), nil
	}

	stream := makeStream(
		llm.StreamChunk{ContentDelta: "start ", FinishReason: "max_tokens"},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, contCount := autoContinueStream(context.Background(), stream, req, invoke, slog.Default(), -1)
	collectStream(out)

	if contCount == nil {
		t.Fatal("expected non-nil continuation count pointer")
	} else if got := int(*contCount); got != 3 {
		t.Errorf("expected continuation count 3, got %d", got)
	}
}

func TestAutoContinueStream_BudgetLimitRespected(t *testing.T) {
	invokeCount := 0
	invoke := func(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokeCount++
		return makeStream(
			llm.StreamChunk{ContentDelta: fmt.Sprintf("cont%d ", invokeCount), FinishReason: "max_tokens"},
		), nil
	}

	stream := makeStream(
		llm.StreamChunk{ContentDelta: "start ", FinishReason: "max_tokens"},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, contCount := autoContinueStream(context.Background(), stream, req, invoke, slog.Default(), 2)
	chunks := collectStream(out)

	if invokeCount != 2 {
		t.Errorf("expected 2 invocations, got %d", invokeCount)
	}
	if got := int(*contCount); got != 2 {
		t.Errorf("expected continuation count 2, got %d", got)
	}

	lastChunk := chunks[len(chunks)-1]
	if lastChunk.FinishReason != "max_tokens" {
		t.Errorf("expected final max_tokens pass-through, got %q", lastChunk.FinishReason)
	}
}

func TestAutoContinueStream_ZeroBudgetNoContinuations(t *testing.T) {
	stream := makeStream(
		llm.StreamChunk{ContentDelta: "truncated", FinishReason: "max_tokens"},
	)

	req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}}}
	out, contCount := autoContinueStream(context.Background(), stream, req, noopInvoker(t), slog.Default(), 0)
	chunks := collectStream(out)

	if got := int(*contCount); got != 0 {
		t.Errorf("expected 0 continuations, got %d", got)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].FinishReason != "max_tokens" {
		t.Errorf("expected max_tokens pass-through, got %q", chunks[0].FinishReason)
	}
}

func TestOnlyImplicitContinueCalls(t *testing.T) {
	tests := []struct {
		name  string
		calls []llm.ToolCall
		want  bool
	}{
		{"empty", nil, false},
		{"single _continue_generation", []llm.ToolCall{{ID: "c1", Name: "_continue_generation"}}, true},
		{"single _continue", []llm.ToolCall{{ID: "c1", Name: "_continue"}}, true},
		{"multiple continue calls", []llm.ToolCall{{ID: "c1", Name: "_continue_generation"}, {ID: "c2", Name: "_continue"}}, true},
		{"single real tool", []llm.ToolCall{{ID: "c1", Name: "get_weather"}}, false},
		{"continue mixed with real tool", []llm.ToolCall{{ID: "c1", Name: "_continue_generation"}, {ID: "c2", Name: "get_weather"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := onlyImplicitContinueCalls(tc.calls); got != tc.want {
				t.Errorf("onlyImplicitContinueCalls = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAnswerImplicitContinueCalls(t *testing.T) {
	logger := slog.Default()

	t.Run("pure continuation response is untouched", func(t *testing.T) {
		in := []llm.ToolCall{{ID: "c1", Name: "_continue_generation"}}
		filtered, results := answerImplicitContinueCalls(in, false, logger)
		if len(filtered) != 1 || len(results) != 0 {
			t.Errorf("expected pass-through, got %d calls and %d results", len(filtered), len(results))
		}
	})

	t.Run("pure continuation response is answered when the turn has other work", func(t *testing.T) {
		in := []llm.ToolCall{{ID: "c1", Name: "_continue_generation"}}
		filtered, results := answerImplicitContinueCalls(in, true, logger)
		if len(filtered) != 0 || len(results) != 1 {
			t.Fatalf("expected the call answered, got %d calls and %d results", len(filtered), len(results))
		}
		if results[0].ToolCallID != "c1" || !strings.Contains(results[0].Content, "must never invoke it yourself") {
			t.Errorf("expected corrective result for c1, got %+v", results[0])
		}
	})

	t.Run("no continuation calls is untouched", func(t *testing.T) {
		in := []llm.ToolCall{{ID: "c1", Name: "get_weather"}, {ID: "c2", Name: "get_time"}}
		filtered, results := answerImplicitContinueCalls(in, false, logger)
		if len(filtered) != 2 || len(results) != 0 {
			t.Errorf("expected pass-through, got %d calls and %d results", len(filtered), len(results))
		}
	})

	t.Run("mixed continuation calls are answered", func(t *testing.T) {
		in := []llm.ToolCall{
			{ID: "c1", Name: "_continue_generation", Arguments: "{}"},
			{ID: "c2", Name: "get_weather", Arguments: "{}"},
			{ID: "c3", Name: "_continue", Arguments: "{}"},
		}
		filtered, results := answerImplicitContinueCalls(in, false, logger)
		if len(filtered) != 1 || filtered[0].ID != "c2" {
			t.Fatalf("expected only the real tool call to survive, got %+v", filtered)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 synthetic results, got %d", len(results))
		}
		for i, want := range []struct{ id, name string }{{"c1", "_continue_generation"}, {"c3", "_continue"}} {
			r := results[i]
			if r.Role != "tool" || r.ToolCallID != want.id || r.ToolName != want.name {
				t.Errorf("result[%d] = role %q callID %q name %q, want tool/%s/%s", i, r.Role, r.ToolCallID, r.ToolName, want.id, want.name)
			}
			if !strings.Contains(r.Content, "must never invoke it yourself") {
				t.Errorf("result[%d] should carry the corrective message, got %q", i, r.Content)
			}
		}
	})
}

func TestAutoContinueStream_CarriesTheWholeRequest(t *testing.T) {
	continuationTemperature := 0.7
	stream1 := make(chan llm.StreamChunk, 2)
	stream1 <- llm.StreamChunk{ContentDelta: "part one"}
	stream1 <- llm.StreamChunk{FinishReason: "max_tokens"}
	close(stream1)

	var invokedReq *llm.ChatRequest
	invoke := func(_ context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
		invokedReq = req
		stream2 := make(chan llm.StreamChunk, 1)
		stream2 <- llm.StreamChunk{ContentDelta: " part two", FinishReason: "stop"}
		close(stream2)
		return stream2, nil
	}

	req := &llm.ChatRequest{
		Model:         "test",
		Messages:      []llm.ChatMessage{{Role: "user", Content: "write a story"}},
		Tools:         []llm.ToolDefinition{{Name: "_continue_generation"}},
		Reasoning:     &llm.ReasoningConfig{Effort: "high"},
		ModelOverride: &llm.BifrostConfig{Model: "override-model"},
		Observability: llm.ObservabilityContext{ComponentName: "agent-x"},
		Spend:         &llm.SpendAttribution{UserID: "u", TaskID: "t", TraceID: "tr", Surface: "agent", TriggerKind: "chat"},
		ToolChoice:    "auto",
		MaxTokens:     1024,
		Temperature:   &continuationTemperature,
		StopSequences: []string{"END"},
		ExtraParams:   map[string]any{"top_p": 0.9},
	}
	out, _ := autoContinueStream(context.Background(), stream1, req, invoke, slog.Default(), -1)
	collectStream(out)

	if invokedReq == nil {
		t.Fatal("no continuation request was made")
	}
	want := *req
	want.Messages = invokedReq.Messages
	if !reflect.DeepEqual(*invokedReq, want) {
		t.Errorf("continuation request differs from the original beyond its messages:\n got %+v\nwant %+v",
			*invokedReq, want)
	}
}

func TestAutoContinueStream_KeepsTheCacheWriteCountKnownOnlyWhenEveryPartReportedIt(t *testing.T) {
	cases := []struct {
		name                        string
		firstReported, nextReported bool
		want                        bool
	}{
		{name: "both reported", firstReported: true, nextReported: true, want: true},
		{name: "first unreported", firstReported: false, nextReported: true, want: false},
		{name: "continuation unreported", firstReported: true, nextReported: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := makeStream(llm.StreamChunk{ContentDelta: "part one", FinishReason: "max_tokens",
				Usage: &llm.Usage{PromptTokens: 10, CachedTokens: 8, CacheWriteTokens: 8, CacheWritesReported: tc.firstReported}})
			invoke := func(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
				return makeStream(llm.StreamChunk{FinishReason: "stop",
					Usage: &llm.Usage{PromptTokens: 20, CachedTokens: 18, CacheWritesReported: tc.nextReported}}), nil
			}
			req := &llm.ChatRequest{Model: "test", Messages: []llm.ChatMessage{{Role: "user", Content: "write"}}}
			out, _ := autoContinueStream(context.Background(), first, req, invoke, slog.Default(), -1)
			chunks := collectStream(out)

			final := chunks[len(chunks)-1].Usage
			if final == nil || final.CacheWriteTokens != 8 || final.CacheWritesReported != tc.want {
				t.Fatalf("final usage = %+v, want 8 cache writes with CacheWritesReported=%v", final, tc.want)
			}
		})
	}
}

func TestAutoContinueStream_ClosesOnlyAfterTheInnerStreamCloses(t *testing.T) {
	inner := make(chan llm.StreamChunk, 1)
	inner <- llm.StreamChunk{ContentDelta: "done", FinishReason: "stop"}
	out, _ := autoContinueStream(t.Context(), inner, &llm.ChatRequest{}, nil, slog.Default(), -1)

	if chunk := <-out; chunk.FinishReason != "stop" {
		t.Fatalf("first chunk %+v, want the terminal chunk", chunk)
	}
	select {
	case _, ok := <-out:
		t.Fatalf("output ended (ok=%v) while the inner stream was still open", ok)
	case <-time.After(50 * time.Millisecond):
	}
	close(inner)
	if _, ok := <-out; ok {
		t.Fatal("output still open after the inner stream closed")
	}
}
