// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

const (
	gpt5Rejection    = "Unsupported value: 'temperature' does not support 0.3 with this model. Only the default (1) value is supported."
	oSeriesRejection = "Unsupported parameter: 'temperature' is not supported with this model."
	bedrockRejection = "litellm.BadRequestError: BedrockException - {\"message\":\"The model returned the following errors: `temperature` is deprecated for this model.\"}. Received Model Group=claude-opus-4-8"
)

type recordingClient struct {
	mu      sync.Mutex
	replies []reply
	got     []snapshot
}

type reply struct {
	content string
	err     error
}

type snapshot struct {
	temperature   *float64
	model         string
	messages      []ChatMessage
	componentName string
	maxTokens     int
}

func (c *recordingClient) ChatCompletion(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := snapshot{
		model:         req.Model,
		messages:      append([]ChatMessage(nil), req.Messages...),
		componentName: req.Observability.ComponentName,
		maxTokens:     req.MaxTokens,
	}
	if req.Temperature != nil {
		snap.temperature = new(*req.Temperature)
	}
	c.got = append(c.got, snap)
	if len(c.got) > len(c.replies) {
		return nil, errors.New("recordingClient: unexpected extra call")
	}
	r := c.replies[len(c.got)-1]
	if r.err != nil {
		return nil, r.err
	}
	return &ChatResponse{Content: r.content}, nil
}

func (c *recordingClient) ChatCompletionStream(context.Context, *ChatRequest) (<-chan StreamChunk, error) {
	return nil, errors.New("recordingClient: streaming not used by the fallback helper")
}

func (c *recordingClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func req(temp *float64) *ChatRequest {
	return &ChatRequest{
		Model:         "test-model",
		Messages:      []ChatMessage{{Role: "user", Content: "ping"}},
		MaxTokens:     16,
		Temperature:   temp,
		Observability: ObservabilityContext{ComponentName: "unit-test"},
	}
}

func TestFallback_RetryDiffersFromTheFirstCallOnlyInTemperature(t *testing.T) {
	t.Parallel()
	for name, message := range map[string]string{
		"openai_gpt5_rejects_value":    gpt5Rejection,
		"openai_oseries_rejects_param": oSeriesRejection,
		"bedrock_deprecated_via_proxy": bedrockRejection,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &recordingClient{replies: []reply{
				{err: &Error{StatusCode: 400, Message: message}},
				{content: "ok"},
			}}
			resp, err := ChatCompletionWithTemperatureFallback(
				t.Context(), c, req(new(0.3)), nil, "test-model", nil)
			if err != nil {
				t.Fatalf("fallback did not recover: %v", err)
			}
			if resp.Content != "ok" {
				t.Errorf("content = %q, want %q", resp.Content, "ok")
			}
			if c.calls() != 2 {
				t.Fatalf("calls = %d, want 2", c.calls())
			}
			first, retry := c.got[0], c.got[1]
			if first.temperature == nil || *first.temperature != 0.3 {
				t.Errorf("first attempt temperature = %v, want 0.3", first.temperature)
			}
			if retry.temperature != nil {
				t.Errorf("retry temperature = %v, want nil", *retry.temperature)
			}
			if !reflect.DeepEqual(first.messages, retry.messages) || len(retry.messages) == 0 {
				t.Errorf("retry changed or dropped the prompt: %#v", retry.messages)
			}
			if retry.componentName != first.componentName || retry.componentName == "" {
				t.Errorf("retry componentName = %q, want %q", retry.componentName, first.componentName)
			}
			if retry.model != first.model || retry.maxTokens != first.maxTokens {
				t.Errorf("retry altered model/maxTokens: %+v vs %+v", retry, first)
			}
		})
	}
}

func TestFallback_HonoursAcceptedTemperature(t *testing.T) {
	t.Parallel()
	c := &recordingClient{replies: []reply{{content: "ok"}}}
	if _, err := ChatCompletionWithTemperatureFallback(
		t.Context(), c, req(new(0.3)), nil, "test-model", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.calls() != 1 {
		t.Fatalf("calls = %d, want 1 — a success must not retry", c.calls())
	}
	if c.got[0].temperature == nil || *c.got[0].temperature != 0.3 {
		t.Errorf("temperature = %v, want 0.3 — the caller's value was dropped", c.got[0].temperature)
	}
}

func TestFallback_NoRetryOnUnrelatedError(t *testing.T) {
	t.Parallel()
	for name, injected := range map[string]error{
		"content filter 400":      &Error{StatusCode: 400, Message: "The response was filtered due to a content management policy"},
		"temperature worded 503":  &Error{StatusCode: 503, Message: "`temperature` is deprecated for this model"},
		"untyped transport error": errors.New("dial tcp: connection refused"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := &recordingClient{replies: []reply{{err: injected}}}
			if _, err := ChatCompletionWithTemperatureFallback(
				t.Context(), c, req(new(0.3)), nil, "m", nil); err == nil {
				t.Fatal("expected the error to propagate")
			}
			if c.calls() != 1 {
				t.Errorf("calls = %d, want 1 — only a temperature rejection warrants a retry", c.calls())
			}
		})
	}
}

func TestFallback_JoinsBothErrors(t *testing.T) {
	t.Parallel()
	rejection := &Error{StatusCode: 400, Message: gpt5Rejection}
	rateLimit := &Error{StatusCode: 429, Message: "rate limit exceeded"}
	c := &recordingClient{replies: []reply{{err: rejection}, {err: rateLimit}}}

	_, err := ChatCompletionWithTemperatureFallback(t.Context(), c, req(new(0.3)), nil, "m", nil)
	if err == nil {
		t.Fatal("expected an error when the retry also fails")
	}
	if c.calls() != 2 {
		t.Errorf("calls = %d, want exactly 2 — bounded, not looped", c.calls())
	}
	if !errors.Is(err, rejection) {
		t.Errorf("temperature rejection lost from the error: %v", err)
	}
	if !errors.Is(err, rateLimit) {
		t.Errorf("retry error lost from the error: %v", err)
	}
}

func TestFallback_MemoizesPerKey(t *testing.T) {
	t.Parallel()
	c := &recordingClient{replies: []reply{
		{err: &Error{StatusCode: 400, Message: gpt5Rejection}},
		{content: "1"}, {content: "2"}, {content: "3"},
	}}
	policy := &TemperaturePolicy{}
	for range 3 {
		if _, err := ChatCompletionWithTemperatureFallback(
			t.Context(), c, req(new(0.3)), policy, "locked", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if c.calls() != 4 {
		t.Fatalf("calls = %d, want 4 (probe+retry once, then one each)", c.calls())
	}
	for i, snap := range c.got[2:] {
		if snap.temperature != nil {
			t.Errorf("call %d sent temperature %v after the verdict", i+3, *snap.temperature)
		}
	}
}

func TestFallback_MemoIsPerKey(t *testing.T) {
	t.Parallel()
	policy := &TemperaturePolicy{}
	policy.Mark("locked-model")

	c := &recordingClient{replies: []reply{{content: "ok"}}}
	if _, err := ChatCompletionWithTemperatureFallback(
		t.Context(), c, req(new(0.3)), policy, "other-model", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.got[0].temperature == nil || *c.got[0].temperature != 0.3 {
		t.Errorf("unrelated key lost its temperature: %v", c.got[0].temperature)
	}
}

func TestFallback_ForgetReprobes(t *testing.T) {
	t.Parallel()
	policy := &TemperaturePolicy{}
	policy.Mark("rotating")
	if !policy.Rejects("rotating") {
		t.Fatal("Mark did not take effect")
	}
	policy.Forget("rotating")

	c := &recordingClient{replies: []reply{{content: "ok"}}}
	if _, err := ChatCompletionWithTemperatureFallback(
		t.Context(), c, req(new(0.3)), policy, "rotating", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.got[0].temperature == nil || *c.got[0].temperature != 0.3 {
		t.Errorf("after Forget temperature = %v, want 0.3 re-probed", c.got[0].temperature)
	}
}

func TestFallback_NilTemperatureUntouched(t *testing.T) {
	t.Parallel()
	policy := &TemperaturePolicy{}
	c := &recordingClient{replies: []reply{{err: &Error{StatusCode: 400, Message: gpt5Rejection}}}}

	if _, err := ChatCompletionWithTemperatureFallback(
		t.Context(), c, req(nil), policy, "m", nil); err == nil {
		t.Fatal("expected the error to propagate")
	}
	if c.calls() != 1 {
		t.Errorf("calls = %d, want 1 — nothing to retry without", c.calls())
	}
	if policy.Rejects("m") {
		t.Error("marked a key as rejecting when no temperature was ever sent")
	}
}

func TestFallback_NilResponseIsAnError(t *testing.T) {
	t.Parallel()
	c := &nilResponseClient{}
	if _, err := ChatCompletionWithTemperatureFallback(
		t.Context(), c, req(new(0.3)), nil, "m", nil); err == nil {
		t.Fatal("expected an error for a nil response with nil error")
	}
}

type nilResponseClient struct{}

func (nilResponseClient) ChatCompletion(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, nil
}
func (nilResponseClient) ChatCompletionStream(context.Context, *ChatRequest) (<-chan StreamChunk, error) {
	return nil, nil
}

func TestTemperaturePolicy_NilIsUsable(t *testing.T) {
	t.Parallel()
	var policy *TemperaturePolicy
	if policy.Rejects("m") {
		t.Error("nil policy should reject nothing")
	}
	policy.Mark("m")
	policy.Forget("m")
	if policy.Rejects("m") {
		t.Error("nil policy must not retain a mark")
	}
}

func TestTemperaturePolicy_ConcurrentUse(t *testing.T) {
	t.Parallel()
	policy := &TemperaturePolicy{}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			key := "m"
			if i%2 == 0 {
				policy.Mark(key)
			} else {
				_ = policy.Rejects(key)
			}
		})
	}
	wg.Wait()
	if !policy.Rejects("m") {
		t.Error("expected the key to be marked after concurrent writers")
	}
}

func TestFallback_MarksOnRejectionNotOnRecovery(t *testing.T) {
	t.Parallel()
	policy := &TemperaturePolicy{}
	c := &recordingClient{replies: []reply{
		{err: &Error{StatusCode: 400, Message: gpt5Rejection}},
		{err: &Error{StatusCode: 429, Message: "rate limit exceeded"}},
	}}

	if _, err := ChatCompletionWithTemperatureFallback(
		t.Context(), c, req(new(0.3)), policy, "locked", nil); err == nil {
		t.Fatal("expected an error when the retry fails")
	}
	if !policy.Rejects("locked") {
		t.Error("the rejection was not recorded because the retry failed; the next call will re-probe pointlessly")
	}
}
