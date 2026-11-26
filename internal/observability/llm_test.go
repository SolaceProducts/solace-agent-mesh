// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func newReq(model string, override *llm.BifrostConfig) *llm.ChatRequest {
	return &llm.ChatRequest{
		Model:         model,
		Messages:      []llm.ChatMessage{{Role: "user", Content: "hi"}},
		ModelOverride: override,
	}
}

func TestEffectiveModel_NoOverride_UsesReqModel(t *testing.T) {
	req := newReq("req-model", nil)
	if got := req.EffectiveModel("bound-model"); got != "req-model" {
		t.Errorf("EffectiveModel = %q, want req-model", got)
	}
}

func TestEffectiveModel_NoOverride_NoReqModel_UsesBound(t *testing.T) {
	req := newReq("", nil)
	if got := req.EffectiveModel("bound-model"); got != "bound-model" {
		t.Errorf("EffectiveModel = %q, want bound-model", got)
	}
}

func TestEffectiveModel_WithOverride_UsesOverrideModel(t *testing.T) {
	req := newReq("req-model", &llm.BifrostConfig{Model: "override-model"})
	if got := req.EffectiveModel("bound-model"); got != "override-model" {
		t.Errorf("EffectiveModel = %q, want override-model", got)
	}
}

func TestEffectiveModel_WithOverrideEmptyModel_FallsThrough(t *testing.T) {
	req := newReq("req-model", &llm.BifrostConfig{Model: ""})
	if got := req.EffectiveModel("bound-model"); got != "req-model" {
		t.Errorf("EffectiveModel = %q, want req-model (fallthrough)", got)
	}
}

type stubClient struct {
	last *llm.ChatRequest
	resp *llm.ChatResponse
}

func (s *stubClient) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	s.last = req
	return s.resp, nil
}

func (s *stubClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

type scriptedStreamClient struct {
	chunks   []llm.StreamChunk
	setupErr error
}

func (s *scriptedStreamClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, nil
}

func (s *scriptedStreamClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if s.setupErr != nil {
		return nil, s.setupErr
	}
	ch := make(chan llm.StreamChunk, len(s.chunks))
	for _, c := range s.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func drainStream(t *testing.T, ch <-chan llm.StreamChunk) []llm.StreamChunk {
	t.Helper()
	var got []llm.StreamChunk
	for c := range ch {
		got = append(got, c)
	}
	return got
}

func TestInstrumented_ModelLabels_WithOverride(t *testing.T) {
	stub := &stubClient{resp: &llm.ChatResponse{
		Content: "ok",
		Usage:   &llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	}}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "default-model")

	req := newReq("", &llm.BifrostConfig{Model: "override-model"})
	_, err := wrapped.ChatCompletion(t.Context(), req)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	if !rec.HasHistogram("gen_ai.client.operation.duration",
		"gen_ai.request.model", "override-model",
	) {
		t.Errorf("expected duration histogram labeled override-model, got %+v",
			rec.Histograms("gen_ai.client.operation.duration"))
	}
	if rec.CounterSum("gen_ai.tokens.used",
		"gen_ai.request.model", "override-model",
		"gen_ai.token.type", "input",
	) != 1 {
		t.Errorf("expected 1 input token recorded for override-model, got counters: %+v",
			rec.Counters("gen_ai.tokens.used"))
	}
}

func TestInstrumented_ModelLabels_WithoutOverride(t *testing.T) {
	stub := &stubClient{resp: &llm.ChatResponse{
		Content: "ok",
		Usage:   &llm.Usage{PromptTokens: 5, CompletionTokens: 6, TotalTokens: 11},
	}}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "default-model")

	req := newReq("req-model", nil)
	_, err := wrapped.ChatCompletion(t.Context(), req)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	if !rec.HasHistogram("gen_ai.client.operation.duration",
		"gen_ai.request.model", "req-model",
	) {
		t.Error("expected duration histogram labeled req-model")
	}
}

func TestInstrumented_TokenCounter_CarriesComponentAndOwner(t *testing.T) {
	stub := &stubClient{resp: &llm.ChatResponse{
		Content: "ok",
		Usage: &llm.Usage{
			PromptTokens:     20,
			CachedTokens:     10,
			CacheWriteTokens: 4,
			CompletionTokens: 11,
			TotalTokens:      31,
		},
	}}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "default-model")

	req := newReq("claude-sonnet", nil)
	req.Observability = llm.ObservabilityContext{
		ComponentName: "agent-Orchestrator",
		OwnerID:       "user-42",
	}
	if _, err := wrapped.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	counters := rec.Counters("gen_ai.tokens.used")
	if len(counters) != 4 {
		t.Fatalf("expected 4 token counters (input, cache_read, cache_write, output), got %d", len(counters))
	}
	for _, c := range counters {
		if got := c.Labels["component.name"]; got != "agent-Orchestrator" {
			t.Errorf("counter[%s] component.name = %q, want agent-Orchestrator",
				c.Labels["gen_ai.token.type"], got)
		}
		if got := c.Labels["owner.id"]; got != "user-42" {
			t.Errorf("counter[%s] owner.id = %q, want user-42",
				c.Labels["gen_ai.token.type"], got)
		}
		if got := c.Labels["gen_ai.request.model"]; got != "claude-sonnet" {
			t.Errorf("counter[%s] gen_ai.request.model = %q, want claude-sonnet",
				c.Labels["gen_ai.token.type"], got)
		}
	}
}

func TestInstrumented_TokenCounter_EmptyObservabilityStillEmits(t *testing.T) {
	stub := &stubClient{resp: &llm.ChatResponse{
		Content: "ok",
		Usage:   &llm.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "default-model")

	req := newReq("claude-sonnet", nil)
	if _, err := wrapped.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	counters := rec.Counters("gen_ai.tokens.used")
	if len(counters) != 2 {
		t.Fatalf("expected 2 token counters, got %d", len(counters))
	}
	for _, c := range counters {
		if got := c.Labels["component.name"]; got != "" {
			t.Errorf("expected empty component.name when Observability unset, got %q", got)
		}
		if got := c.Labels["owner.id"]; got != "" {
			t.Errorf("expected empty owner.id when Observability unset, got %q", got)
		}
	}
}

func TestInstrumented_StreamingEmitsTTFTAndDuration(t *testing.T) {
	stub := &scriptedStreamClient{
		chunks: []llm.StreamChunk{
			{ContentDelta: "hel"},
			{ContentDelta: "lo"},
			{Usage: &llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}, FinishReason: "stop"},
		},
	}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ch, err := wrapped.ChatCompletionStream(t.Context(), newReq("", nil))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	if got := len(drainStream(t, ch)); got != 3 {
		t.Errorf("forwarded %d chunks, want 3", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for rec.HistogramCount("gen_ai.client.operation.duration") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for duration histogram (goroutine may be stuck)")
		}
		time.Sleep(2 * time.Millisecond)
	}

	if !rec.HasHistogram("gen_ai.client.operation.ttft.duration",
		"gen_ai.request.model", "claude-sonnet") {
		t.Errorf("missing TTFT histogram. all histograms: %+v", rec.Histograms("gen_ai.client.operation.ttft.duration"))
	}
	if !rec.HasHistogram("gen_ai.client.operation.duration",
		"gen_ai.request.model", "claude-sonnet") {
		t.Errorf("missing duration histogram")
	}

	if got := rec.CounterSum("gen_ai.tokens.used",
		"gen_ai.request.model", "claude-sonnet",
		"gen_ai.token.type", "input"); got != 3 {
		t.Errorf("input tokens = %d, want 3", got)
	}
	if got := rec.CounterSum("gen_ai.tokens.used",
		"gen_ai.request.model", "claude-sonnet",
		"gen_ai.token.type", "output"); got != 5 {
		t.Errorf("output tokens = %d, want 5", got)
	}
}

func TestInstrumented_StreamingChunkErrorLabelsDuration(t *testing.T) {
	stub := &scriptedStreamClient{
		chunks: []llm.StreamChunk{
			{ContentDelta: "partial"},
			{Err: errors.New("rate limit exceeded")},
		},
	}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ch, err := wrapped.ChatCompletionStream(t.Context(), newReq("", nil))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	drainStream(t, ch)

	if !rec.HasHistogram("gen_ai.client.operation.ttft.duration") {
		t.Error("expected TTFT histogram from first content chunk")
	}
	hists := rec.Histograms("gen_ai.client.operation.duration")
	if len(hists) != 1 {
		t.Fatalf("expected 1 duration histogram, got %d", len(hists))
	}
	if got := hists[0].Labels["error.type"]; got != "rate_limit" {
		t.Errorf("duration error.type = %q, want rate_limit", got)
	}
}

func TestInstrumented_StreamSetupErrorSkipsTTFT(t *testing.T) {
	stub := &scriptedStreamClient{setupErr: errors.New("upstream unavailable")}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ch, err := wrapped.ChatCompletionStream(t.Context(), newReq("", nil))
	if err == nil {
		t.Fatal("expected setup error from wrapped stream")
	}
	if ch != nil {
		t.Error("expected nil channel when setup fails")
	}

	if got := rec.Histograms("gen_ai.client.operation.ttft.duration"); len(got) != 0 {
		t.Errorf("TTFT should not fire on setup failure, got %+v", got)
	}
	hists := rec.Histograms("gen_ai.client.operation.duration")
	if len(hists) != 1 {
		t.Fatalf("expected duration histogram on setup error, got %d", len(hists))
	}
	if got := hists[0].Labels["error.type"]; got != "error" {
		t.Errorf("setup-error duration error.type = %q, want \"error\"", got)
	}
}

func TestInstrumented_StreamCancellationLabelsDurationAndRecordsTokens(t *testing.T) {
	chunks := make([]llm.StreamChunk, 0, 20)
	chunks = append(chunks, llm.StreamChunk{
		ContentDelta: "x",
		Usage:        &llm.Usage{PromptTokens: 9, CompletionTokens: 4, TotalTokens: 13},
	})
	for range 19 {
		chunks = append(chunks, llm.StreamChunk{ContentDelta: "y"})
	}
	stub := &scriptedStreamClient{chunks: chunks}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := wrapped.ChatCompletionStream(ctx, newReq("", nil)); err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for rec.HistogramCount("gen_ai.client.operation.duration") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for duration histogram after cancellation (goroutine may be stuck)")
		}
		time.Sleep(2 * time.Millisecond)
	}

	hists := rec.Histograms("gen_ai.client.operation.duration")
	if got := hists[0].Labels["error.type"]; got != "error" {
		t.Errorf("cancelled-stream duration error.type = %q, want \"error\" (context.Canceled); empty would mean it was mislabeled as success", got)
	}
	if got := rec.CounterSum("gen_ai.tokens.used", "gen_ai.token.type", "input"); got != 9 {
		t.Errorf("input tokens after cancellation = %d, want 9 (usage observed pre-cancel must not be dropped)", got)
	}
	if got := rec.CounterSum("gen_ai.tokens.used", "gen_ai.token.type", "output"); got != 4 {
		t.Errorf("output tokens after cancellation = %d, want 4", got)
	}
}

func tokenCountsByType(rec *RecordingMetrics) map[string]int64 {
	counts := map[string]int64{}
	for _, c := range rec.Counters("gen_ai.tokens.used") {
		counts[c.Labels["gen_ai.token.type"]] += c.Delta
	}
	return counts
}

func assertTokenCounts(t *testing.T, rec *RecordingMetrics, want map[string]int64) {
	t.Helper()
	if entries := len(rec.Counters("gen_ai.tokens.used")); entries != len(want) {
		t.Errorf("token counter entries = %d, want one per type (%d)", entries, len(want))
	}
	got := tokenCountsByType(rec)
	if len(got) != len(want) {
		t.Errorf("token types = %v, want %v", got, want)
	}
	for tokenType, count := range want {
		if got[tokenType] != count {
			t.Errorf("%s tokens = %d, want %d (all: %v)", tokenType, got[tokenType], count, got)
		}
	}
}

func TestInstrumented_TokenCounter_SplitsCacheBuckets(t *testing.T) {
	stub := &stubClient{resp: &llm.ChatResponse{
		Content: "ok",
		Usage: &llm.Usage{
			PromptTokens:     1000,
			CachedTokens:     800,
			CacheWriteTokens: 300,
			CompletionTokens: 50,
			TotalTokens:      1050,
		},
	}}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	if _, err := wrapped.ChatCompletion(t.Context(), newReq("", nil)); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	assertTokenCounts(t, rec, map[string]int64{
		"input":       200,
		"cache_read":  500,
		"cache_write": 300,
		"output":      50,
	})
}

func TestInstrumented_TokenCounter_ClampsInconsistentCacheCounts(t *testing.T) {
	tests := []struct {
		name  string
		usage llm.Usage
		want  map[string]int64
	}{
		{
			name:  "cache write exceeds cached",
			usage: llm.Usage{PromptTokens: 100, CachedTokens: 40, CacheWriteTokens: 60, CompletionTokens: 10},
			want:  map[string]int64{"input": 60, "cache_write": 40, "output": 10},
		},
		{
			name:  "cached exceeds prompt",
			usage: llm.Usage{PromptTokens: 100, CachedTokens: 120, CacheWriteTokens: 150, CompletionTokens: 10},
			want:  map[string]int64{"cache_write": 100, "output": 10},
		},
		{
			name:  "negative counts",
			usage: llm.Usage{PromptTokens: -5, CachedTokens: -1, CacheWriteTokens: -1, CompletionTokens: 10},
			want:  map[string]int64{"output": 10},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := tt.usage
			stub := &stubClient{resp: &llm.ChatResponse{Content: "ok", Usage: &usage}}
			rec := installMetrics(t, NewRecordingMetrics())
			wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

			if _, err := wrapped.ChatCompletion(t.Context(), newReq("", nil)); err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}

			assertTokenCounts(t, rec, tt.want)
		})
	}
}

func TestInstrumented_StreamRecordsUsageWithZeroTotal(t *testing.T) {
	stub := &scriptedStreamClient{
		chunks: []llm.StreamChunk{
			{ContentDelta: "hi"},
			{Usage: &llm.Usage{PromptTokens: 12, CachedTokens: 4, CompletionTokens: 3}, FinishReason: "stop"},
		},
	}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ch, err := wrapped.ChatCompletionStream(t.Context(), newReq("", nil))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	drainStream(t, ch)

	assertTokenCounts(t, rec, map[string]int64{
		"input":      8,
		"cache_read": 4,
		"output":     3,
	})
}

func TestInstrumented_StreamKeepsUsageWhenLaterChunkReportsZero(t *testing.T) {
	stub := &scriptedStreamClient{
		chunks: []llm.StreamChunk{
			{Usage: &llm.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15}},
			{Usage: &llm.Usage{}, FinishReason: "stop"},
		},
	}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ch, err := wrapped.ChatCompletionStream(t.Context(), newReq("", nil))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	drainStream(t, ch)

	assertTokenCounts(t, rec, map[string]int64{
		"input":  12,
		"output": 3,
	})
}

func TestInstrumented_StreamKeepsUsageWhenLaterChunkHasNoBucketedTokens(t *testing.T) {
	stub := &scriptedStreamClient{
		chunks: []llm.StreamChunk{
			{Usage: &llm.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15}},
			{Usage: &llm.Usage{CachedTokens: 5}, FinishReason: "stop"},
		},
	}
	rec := installMetrics(t, NewRecordingMetrics())
	wrapped := NewInstrumentedLLMClient(stub, "claude-sonnet")

	ch, err := wrapped.ChatCompletionStream(t.Context(), newReq("", nil))
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	drainStream(t, ch)

	assertTokenCounts(t, rec, map[string]int64{
		"input":  12,
		"output": 3,
	})
}
