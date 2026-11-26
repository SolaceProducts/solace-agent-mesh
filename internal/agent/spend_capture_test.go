// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const spendTestNamespace = "spend-ns"

var spendTestUsage = &llm.Usage{
	PromptTokens: 1000, CachedTokens: 600, CacheWriteTokens: 200, CompletionTokens: 50, TotalTokens: 1050,
}

type spendFixture struct {
	agent     *Agent
	publisher *spend.Publisher
	broker    *broker.DevBroker
	queue     broker.Queue
}

func newSpendFixture(t *testing.T, withPublisher bool) *spendFixture {
	t.Helper()
	var responses []*llm.ChatResponse
	for range 8 {
		responses = append(responses, &llm.ChatResponse{Content: "summary", Usage: spendTestUsage})
	}
	return newSpendFixtureAnswering(t, withPublisher, responses...)
}

func newSpendFixtureAnswering(t *testing.T, withPublisher bool, responses ...*llm.ChatResponse) *spendFixture {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	q, err := brk.CreateQueue("spend-test", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.SpendEventTopic(spendTestNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	agent := New(AgentConfig{Name: "spend-agent", Model: "agent-model", ModelAlias: "agent-alias"})
	svc := newMockServices(newMockLLM(responses...), newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}

	f := &spendFixture{agent: agent, broker: brk, queue: q}
	if withPublisher {
		f.publisher = spend.NewPublisher(brk, spendTestNamespace, agent.logger)
		agent.SetSpendPublisher(f.publisher)
		t.Cleanup(func() { _ = f.publisher.Close(context.Background()) })
	}
	return f
}

func (f *spendFixture) recordedCount(t *testing.T, want int) []spend.Event {
	t.Helper()
	var events []spend.Event
	receive := func(timeout time.Duration) bool {
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		defer cancel()
		msg, err := f.broker.Receive(ctx, f.queue)
		if err != nil {
			return false
		}
		var e spend.Event
		if err := json.Unmarshal(msg.Payload, &e); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events = append(events, e)
		return true
	}
	for len(events) < want && receive(5*time.Second) {
	}
	if f.publisher != nil {
		if err := f.publisher.Close(t.Context()); err != nil {
			t.Fatalf("close publisher: %v", err)
		}
	}
	for receive(20 * time.Millisecond) {
	}
	return events
}

func spendAttribution() *llm.SpendAttribution {
	return &llm.SpendAttribution{
		UserSub: "user-1", AgentName: "agent-1", TriggerKind: "chat", Surface: "agent",
		TaskID: "task-1", TraceID: "trace-1",
	}
}

func TestAgentRecordsSpendOfItsLLMCallsOnlyWhenItHasAPublisher(t *testing.T) {
	cases := map[string]bool{"publisher set": true, "no publisher": false}
	for name, withPublisher := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSpendFixture(t, withPublisher)

			if _, err := f.agent.llmClient(t.Context()).ChatCompletion(t.Context(), &llm.ChatRequest{Spend: spendAttribution()}); err != nil {
				t.Fatalf("call: %v", err)
			}

			want := 0
			if withPublisher {
				want = 1
			}
			events := f.recordedCount(t, want)
			if !withPublisher {
				if len(events) != 0 {
					t.Errorf("recorded %+v without a publisher", events)
				}
				return
			}
			if len(events) != 1 || events[0].ModelName != "openai/agent-model" || events[0].Tokens.CacheWrite != 200 {
				t.Errorf("events = %+v, want one on openai/agent-model with 200 cache-write tokens", events)
			}
		})
	}
}

func TestAutoContinuedStreamRecordsEachPhysicalCallWithItsOwnUsage(t *testing.T) {
	firstUsage := &llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}
	secondUsage := &llm.Usage{PromptTokens: 300, CachedTokens: 100, CompletionTokens: 40, TotalTokens: 340}
	f := newSpendFixtureAnswering(t, true,
		&llm.ChatResponse{Content: "part one", FinishReason: "max_tokens", Usage: firstUsage},
		&llm.ChatResponse{Content: " part two", FinishReason: "stop", Usage: secondUsage},
	)
	sp := NewStatusPublisher(nil, StatusPublisherConfig{}, f.agent.logger)

	resp, _, err := f.agent.streamLLMCall(t.Context(), sp, &llm.ChatRequest{Spend: spendAttribution()}, &streamCallInfo{
		UserID: "user-1", SessionID: "session-1", MaxContinuations: -1,
	})
	if err != nil {
		t.Fatalf("streamLLMCall: %v", err)
	}

	if resp.Content != "part one part two" {
		t.Fatalf("content = %q, want both parts, so the turn really continued", resp.Content)
	}
	events := f.recordedCount(t, 2)
	if len(events) != 2 {
		t.Fatalf("got %d events, want one per physical call: %+v", len(events), events)
	}
	got := map[spend.Tokens]bool{events[0].Tokens: true, events[1].Tokens: true}
	for _, want := range []spend.Tokens{
		{UncachedInput: 100, Output: 20},
		{UncachedInput: 200, Output: 40, CacheRead: 100},
	} {
		if !got[want] {
			t.Errorf("no event with tokens %+v among %+v", want, events)
		}
	}
}

func TestAgentRecordsSpendOfInternalLLMCallsOnBothClientPaths(t *testing.T) {
	f := newSpendFixture(t, true)

	for _, model := range []string{"agent-model", "other-model"} {
		req := &llm.ChatRequest{Model: model}
		if _, err := f.agent.runInternalLLMCall(t.Context(), internalCallSinks{attribution: spendAttribution()}, req, "internal call"); err != nil {
			t.Fatalf("internal call on %s: %v", model, err)
		}
	}

	events := f.recordedCount(t, 2)
	if len(events) != 2 {
		t.Fatalf("got %d events, want one per internal call: %+v", len(events), events)
	}
	models := map[string]bool{events[0].ModelName: true, events[1].ModelName: true}
	if !models["openai/agent-model"] || !models["openai/other-model"] {
		t.Errorf("event models = %v, want openai/agent-model and openai/other-model", models)
	}
}
