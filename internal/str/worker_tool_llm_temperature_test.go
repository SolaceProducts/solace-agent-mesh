// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const gpt5TemperatureRejection = "Unsupported value: 'temperature' does not support 0.3 with this model. Only the default (1) value is supported."

const oSeriesTemperatureRejection = "Unsupported parameter: 'temperature' is not supported with this model."

const bedrockTemperatureRejection = `litellm.BadRequestError: BedrockException - {"message":"The model returned the following errors: ` +
	"`temperature`" + ` is deprecated for this model."}. Received Model Group=claude-opus-4-8`

type scriptedLLMClient struct {
	replies []scriptedReply
	calls   int
	got     []recordedRequest
}

type scriptedReply struct {
	content string
	err     error
}

type recordedRequest struct {
	temperature   *float64
	messages      []llm.ChatMessage
	componentName string
}

func (c *scriptedLLMClient) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	rec := recordedRequest{
		messages:      append([]llm.ChatMessage(nil), req.Messages...),
		componentName: req.Observability.ComponentName,
	}
	if req.Temperature != nil {
		rec.temperature = new(*req.Temperature)
	}
	c.got = append(c.got, rec)
	c.calls++
	if c.calls > len(c.replies) {
		return nil, errors.New("scriptedLLMClient: unexpected extra call")
	}
	r := c.replies[c.calls-1]
	if r.err != nil {
		return nil, r.err
	}
	return &llm.ChatResponse{Content: r.content}, nil
}

func (c *scriptedLLMClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("scriptedLLMClient: streaming not used by the tool LLM callback")
}

func assertOnlyTemperatureChanged(t *testing.T, got []recordedRequest) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(got))
	}
	first, retry := got[0], got[1]
	if !reflect.DeepEqual(first.messages, retry.messages) {
		t.Errorf("retry changed the prompt:\n first = %#v\n retry = %#v", first.messages, retry.messages)
	}
	if len(retry.messages) == 0 {
		t.Error("retry sent no messages — a reply generated from an empty prompt would look like success")
	}
	if retry.componentName != first.componentName {
		t.Errorf("retry componentName = %q, want %q — tool LLM spend is attributed by this field",
			retry.componentName, first.componentName)
	}
	if retry.componentName == "" {
		t.Error("retry lost Observability.ComponentName")
	}
}

func toolLLMCallbackFor(t *testing.T, client llm.Client, w *Worker) toolLLMCallFunc {
	t.Helper()
	if w == nil {
		w = NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
	}
	cb := w.newToolLLMCallback(client)
	if cb == nil {
		t.Fatal("newToolLLMCallback returned nil with a non-nil env client")
	}
	return cb
}

func TestToolLLMCallback_RetriesWithoutTemperatureOnRejection(t *testing.T) {
	t.Parallel()
	for name, message := range map[string]string{
		"openai_gpt5":       gpt5TemperatureRejection,
		"openai_o_series":   oSeriesTemperatureRejection,
		"bedrock_via_proxy": bedrockTemperatureRejection,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &scriptedLLMClient{replies: []scriptedReply{
				{err: &llm.Error{StatusCode: 400, Message: message}},
				{content: "test plan"},
			}}
			cb := toolLLMCallbackFor(t, client, nil)

			got, err := cb(t.Context(), "", "generate_test_plan", nil, "sys", "user", 0.3)
			if err != nil {
				t.Fatalf("callback should have recovered by retrying without temperature: %v", err)
			}
			if got != "test plan" {
				t.Errorf("content = %q, want %q", got, "test plan")
			}
			if client.calls != 2 {
				t.Fatalf("calls = %d, want 2 (one rejected, one retried)", client.calls)
			}
			if client.got[0].temperature == nil || *client.got[0].temperature != 0.3 {
				t.Errorf("first attempt temperature = %v, want 0.3 — the tool's value must be tried first",
					client.got[0].temperature)
			}
			if client.got[1].temperature != nil {
				t.Errorf("retry temperature = %v, want nil — an explicit value is what the model rejected",
					*client.got[1].temperature)
			}
			assertOnlyTemperatureChanged(t, client.got)
		})
	}
}

func TestToolLLMCallback_HonoursTemperatureWhenAccepted(t *testing.T) {
	t.Parallel()
	client := &scriptedLLMClient{replies: []scriptedReply{{content: "ok"}}}
	cb := toolLLMCallbackFor(t, client, nil)

	if _, err := cb(t.Context(), "", "generate_recommendations", nil, "sys", "user", 0.2); err != nil {
		t.Fatalf("callback: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("calls = %d, want 1 — a successful call must not retry", client.calls)
	}
	if client.got[0].temperature == nil || *client.got[0].temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2 — the tool's value was dropped", client.got[0].temperature)
	}
}

func TestToolLLMCallback_NoRetryOnUnrelatedError(t *testing.T) {
	t.Parallel()
	for name, injected := range map[string]error{
		"content filter 400":       &llm.Error{StatusCode: 400, Message: "The response was filtered due to the prompt triggering a content management policy"},
		"5xx is never a rejection": &llm.Error{StatusCode: 503, Message: "`temperature` is deprecated for this model"},
		"untyped":                  errors.New("dial tcp: connection refused"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &scriptedLLMClient{replies: []scriptedReply{{err: injected}}}
			cb := toolLLMCallbackFor(t, client, nil)

			if _, err := cb(t.Context(), "", "tool", nil, "sys", "user", 0.3); err == nil {
				t.Fatal("expected the error to propagate")
			}
			if client.calls != 1 {
				t.Errorf("calls = %d, want 1 — only a temperature rejection warrants a retry", client.calls)
			}
		})
	}
}

func TestToolLLMCallback_RetryFailureJoinsBothErrors(t *testing.T) {
	t.Parallel()
	rejection := &llm.Error{StatusCode: 400, Message: gpt5TemperatureRejection}
	rateLimit := &llm.Error{StatusCode: 429, Message: "rate limit exceeded"}
	client := &scriptedLLMClient{replies: []scriptedReply{{err: rejection}, {err: rateLimit}}}
	cb := toolLLMCallbackFor(t, client, nil)

	_, err := cb(t.Context(), "", "tool", nil, "sys", "user", 0.3)
	if err == nil {
		t.Fatal("expected an error when the retry also fails")
	}
	if client.calls != 2 {
		t.Errorf("calls = %d, want exactly 2 — the fallback must be attempted once, not looped", client.calls)
	}
	if !errors.Is(err, rejection) {
		t.Errorf("temperature rejection was lost from the returned error: %v", err)
	}
	if !errors.Is(err, rateLimit) {
		t.Errorf("retry error was lost from the returned error: %v", err)
	}
}

func TestToolLLMCallback_MemoizesRejectionPerAlias(t *testing.T) {
	t.Parallel()
	client := &scriptedLLMClient{replies: []scriptedReply{
		{err: &llm.Error{StatusCode: 400, Message: gpt5TemperatureRejection}},
		{content: "first"},
		{content: "second"},
		{content: "third"},
	}}
	w := NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
	cb := toolLLMCallbackFor(t, client, w)

	for range 3 {
		if _, err := cb(t.Context(), "", "generate_test_plan", nil, "sys", "user", 0.3); err != nil {
			t.Fatalf("callback: %v", err)
		}
	}

	if client.calls != 4 {
		t.Fatalf("calls = %d, want 4 (probe+retry once, then one call each)", client.calls)
	}
	for i, rec := range client.got[2:] {
		if rec.temperature != nil {
			t.Errorf("call %d after the verdict sent temperature %v; the probe should be skipped",
				i+3, *rec.temperature)
		}
	}
}

func TestToolLLMCallback_BindingRotationReprobes(t *testing.T) {
	t.Parallel()
	w := NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
	alias, _ := w.toolModelAlias()
	w.tempRejects.Mark(alias)
	if !w.tempRejects.Rejects(alias) {
		t.Fatal("mark did not take effect")
	}

	w.tempRejects.Forget(alias)

	client := &scriptedLLMClient{replies: []scriptedReply{{content: "ok"}}}
	cb := toolLLMCallbackFor(t, client, w)
	if _, err := cb(t.Context(), "", "tool", nil, "sys", "user", 0.3); err != nil {
		t.Fatalf("callback: %v", err)
	}
	if client.got[0].temperature == nil || *client.got[0].temperature != 0.3 {
		t.Errorf("after rotation temperature = %v, want 0.3 re-probed", client.got[0].temperature)
	}
}

func TestToolLLMCallback_FallbackWarnNamesTool(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	client := &scriptedLLMClient{replies: []scriptedReply{
		{err: &llm.Error{StatusCode: 400, Message: gpt5TemperatureRejection}},
		{content: "ok"},
	}}
	w := NewWorker(&testServices{}, WorkerConfig{WorkerID: "t", Namespace: "test-ns"})
	w.logger = logger

	if _, err := w.newToolLLMCallback(client)(
		t.Context(), "", "generate_test_plan", nil, "sys", "user", 0.3); err != nil {
		t.Fatalf("callback: %v", err)
	}

	var found bool
	for line := range bytes.SplitSeq(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		msg, _ := rec["msg"].(string)
		if !strings.Contains(msg, "rejected an explicit temperature") {
			continue
		}
		found = true
		if got, _ := rec["tool"].(string); got != "generate_test_plan" {
			t.Errorf("fallback WARN tool = %q, want %q — the tool name was lost from the line", got, "generate_test_plan")
		}
		if got, _ := rec["policyKey"].(string); got == "" {
			t.Error("fallback WARN carried no policyKey")
		}
	}
	if !found {
		t.Fatalf("no temperature-fallback WARN was logged; got:\n%s", buf.String())
	}
}
