// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type internalLLMHarness struct {
	agent  *Agent
	sp     *StatusPublisher
	broker broker.Broker
	queue  broker.Queue
}

func newInternalLLMHarness(t *testing.T, client llm.Client, taskID string) *internalLLMHarness {
	t.Helper()
	agent := New(AgentConfig{Name: "test-agent", Model: "test-model"})
	svc := newMockServices(client, newMemSessionStore())
	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mb.Disconnect(t.Context()) })
	svc.brk = mb
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	statusTopic := "test-ns/a2a/v1/agent/status/test-agent/" + taskID
	sp := NewStatusPublisher(mb, StatusPublisherConfig{StatusTopic: statusTopic, TaskID: taskID, ContextID: "ctx-" + taskID, AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())
	q, err := mb.CreateQueue("queue-"+taskID, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}
	return &internalLLMHarness{agent: agent, sp: sp, broker: mb, queue: q}
}

func TestInternalLLMCall_PublishesStatusEvents(t *testing.T) {
	mockClient := newMockLLM(&llm.ChatResponse{
		Content: "summary text",
		Usage:   &llm.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
	})
	h := newInternalLLMHarness(t, mockClient, "task-llm-1")

	got, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "be helpful", "what is 2+2?", 0.3)
	if err != nil {
		t.Fatalf("internalLLMCall: %v", err)
	}
	if got != "summary text" {
		t.Errorf("response = %q, want %q", got, "summary text")
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 2)

	invType, _ := signals[0]["type"].(string)
	if invType != "llm_invocation" {
		t.Errorf("signal[0].type = %q, want llm_invocation", invType)
	}
	req, ok := signals[0]["request"].(map[string]any)
	if !ok {
		t.Fatalf("signal[0].request missing or wrong shape: %+v", signals[0])
	}
	if req["model"] != "test-model" {
		t.Errorf("invocation.model = %v, want test-model", req["model"])
	}
	contents, ok := req["contents"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("invocation.contents missing: %+v", req)
	}

	respType, _ := signals[1]["type"].(string)
	if respType != "llm_response" {
		t.Errorf("signal[1].type = %q, want llm_response", respType)
	}
	usage, ok := signals[1]["usage"].(map[string]any)
	if !ok {
		t.Fatalf("signal[1].usage missing: %+v", signals[1])
	}
	if usage["model"] != "test-model" {
		t.Errorf("response.usage.model = %v, want test-model", usage["model"])
	}
	if usage["input_tokens"].(float64) != 11 {
		t.Errorf("response.usage.input_tokens = %v, want 11", usage["input_tokens"])
	}
	if usage["output_tokens"].(float64) != 7 {
		t.Errorf("response.usage.output_tokens = %v, want 7", usage["output_tokens"])
	}
	respData, ok := signals[1]["data"].(map[string]any)
	if !ok {
		t.Fatalf("signal[1].data missing: %+v", signals[1])
	}
	content, _ := respData["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	if len(parts) == 0 {
		t.Fatalf("response.data.content.parts empty: %+v", respData)
	}
	first, _ := parts[0].(map[string]any)
	if first["text"] != "summary text" {
		t.Errorf("response text = %v, want %q", first["text"], "summary text")
	}
}

func TestInternalLLMCall_ReportsTheCacheWriteCount(t *testing.T) {
	cases := []struct {
		name      string
		usage     llm.Usage
		wantKey   bool
		wantValue float64
	}{
		{
			name:      "writes reported",
			usage:     llm.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105, CachedTokens: 60, CacheWriteTokens: 40, CacheWritesReported: true},
			wantKey:   true,
			wantValue: 40,
		},
		{
			name:    "no writes reported as zero",
			usage:   llm.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105, CachedTokens: 60, CacheWritesReported: true},
			wantKey: true,
		},
		{
			name:  "writes not reported",
			usage: llm.Usage{PromptTokens: 100, CompletionTokens: 5, TotalTokens: 105, CachedTokens: 60},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := newMockLLM(&llm.ChatResponse{Content: "ok", Usage: &tc.usage})
			h := newInternalLLMHarness(t, mockClient, "task-llm-cache")
			if _, err := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.1); err != nil {
				t.Fatalf("internalLLMCall: %v", err)
			}
			usage, _ := drainStatusSignals(t, h.broker, h.queue, 2)[1]["usage"].(map[string]any)
			if usage["cached_input_tokens"] != 60.0 {
				t.Errorf("usage = %v, want cached_input_tokens 60", usage)
			}
			writes, present := usage["cache_write_tokens"]
			if present != tc.wantKey || (tc.wantKey && writes != tc.wantValue) {
				t.Errorf("usage = %v, want cache_write_tokens present=%v value=%v", usage, tc.wantKey, tc.wantValue)
			}
		})
	}
}

func TestInternalLLMCall_NilStatusPublisher_NoCrash(t *testing.T) {
	mockClient := newMockLLM(&llm.ChatResponse{Content: "ok"})
	agent := New(AgentConfig{Name: "test-agent", Model: "test-model"})
	svc := newMockServices(mockClient, newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got, err := agent.internalLLMCall(t.Context(), internalCallSinks{}, "", "sys", "user", 0.1)
	if err != nil {
		t.Fatalf("internalLLMCall: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want ok", got)
	}
}

type errLLMClient struct{ err error }

func (e *errLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, e.err
}

func (e *errLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, e.err
}

var errLLMTestSentinel = errors.New("simulated LLM failure")

func TestInternalLLMCall_OnError_NoResponseEvent(t *testing.T) {
	h := newInternalLLMHarness(t, &errLLMClient{err: errLLMTestSentinel}, "task-err-1")

	_, callErr := h.agent.internalLLMCall(t.Context(), internalCallSinks{sp: h.sp}, "", "sys", "user", 0.1)
	if callErr == nil {
		t.Fatal("expected error from failing LLM client")
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 1)
	if signals[0]["type"] != "llm_invocation" {
		t.Errorf("signal[0].type = %v, want llm_invocation", signals[0]["type"])
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if msg, _ := h.broker.Receive(ctx, h.queue); msg != nil {
		t.Errorf("unexpected second status event after error: %s", msg.Payload)
	}
}

func TestInternalLLMCallMultimodal_PublishesStatusEvents(t *testing.T) {
	mockClient := newMockLLM(&llm.ChatResponse{
		Content: "a red cat",
		Usage:   &llm.Usage{PromptTokens: 100, CompletionTokens: 4, TotalTokens: 104},
	})
	h := newInternalLLMHarness(t, mockClient, "task-mm-1")

	parts := []tool.MultimodalPart{
		tool.TextPart{Text: "what's in this picture?"},
		tool.ImagePart{Data: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, MIMEType: "image/png"},
	}
	temp := 0.0
	got, err := h.agent.internalLLMCallMultimodal(t.Context(), internalCallSinks{sp: h.sp}, "describe images", parts, tool.InternalLLMCallMultimodalOpts{Temperature: &temp})
	if err != nil {
		t.Fatalf("internalLLMCallMultimodal: %v", err)
	}
	if got != "a red cat" {
		t.Errorf("response = %q, want %q", got, "a red cat")
	}

	signals := drainStatusSignals(t, h.broker, h.queue, 2)
	if signals[0]["type"] != "llm_invocation" {
		t.Errorf("signal[0].type = %v, want llm_invocation", signals[0]["type"])
	}
	if signals[1]["type"] != "llm_response" {
		t.Errorf("signal[1].type = %v, want llm_response", signals[1]["type"])
	}

	raw, _ := json.Marshal(signals[0])
	if len(raw) > 4096 {
		t.Errorf("llm_invocation payload unexpectedly large (%d bytes) — sanitization may have failed", len(raw))
	}
	if strings.Contains(string(raw), "iVBORw0KGgo") || strings.Contains(string(raw), "0x89PNG") {
		t.Error("raw image bytes leaked into llm_invocation payload — sanitizeBinaryInPlace failed")
	}
}

func drainStatusSignals(t *testing.T, mb broker.Broker, q broker.Queue, count int) []map[string]any {
	t.Helper()
	signals := make([]map[string]any, 0, count)
	for range count {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := mb.Receive(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("expected status update: %v", err)
		}
		var resp a2a.Response
		if err := json.Unmarshal(msg.Payload, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		resultBytes, _ := json.Marshal(resp.Result)
		var event a2a.TaskStatusUpdateEvent
		if err := json.Unmarshal(resultBytes, &event); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if event.Status.Message == nil || len(event.Status.Message.Parts) == 0 {
			t.Fatal("event has no parts")
		}
		partBytes, _ := json.Marshal(event.Status.Message.Parts[0])
		var dataPart struct {
			Kind string         `json:"kind"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(partBytes, &dataPart); err != nil {
			t.Fatalf("unmarshal data part: %v", err)
		}
		if dataPart.Kind != "data" {
			t.Fatalf("part kind = %q, want data", dataPart.Kind)
		}
		signals = append(signals, dataPart.Data)
	}
	return signals
}
