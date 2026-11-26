// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var gatedPeriodEnd = time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC)

type countingLLM struct {
	mu     sync.Mutex
	calls  int
	resp   *llm.ChatResponse
	stream chan llm.StreamChunk
}

func newCountingLLM() *countingLLM {
	return &countingLLM{resp: &llm.ChatResponse{Content: "ok"}, stream: make(chan llm.StreamChunk)}
}

func (c *countingLLM) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	c.record()
	return c.resp, nil
}

func (c *countingLLM) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	c.record()
	return c.stream, nil
}

func (c *countingLLM) record() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
}

func (c *countingLLM) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func assertExhausted(t *testing.T, err error) {
	t.Helper()
	var exhausted *budget.ExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("error = %v, want a *budget.ExhaustedError", err)
	}
	if !exhausted.ResetsAt.Equal(gatedPeriodEnd) {
		t.Errorf("resets at %v, want the period end %v", exhausted.ResetsAt, gatedPeriodEnd)
	}
}

func TestGatedClient_RefusesBothCallKindsWithoutReachingTheProviderWhileBlocked(t *testing.T) {
	inner := newCountingLLM()
	gate := budgettest.NewGate()
	gate.Apply(t.Context(), budgettest.MeshState(true, gatedPeriodEnd, time.Now()))
	client := NewGatedClient(inner, gate)

	resp, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{})
	assertExhausted(t, err)
	if resp != nil {
		t.Errorf("refused completion returned a response %+v", resp)
	}

	stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{})
	assertExhausted(t, err)
	if stream != nil {
		t.Error("refused stream returned a channel")
	}

	if n := inner.callCount(); n != 0 {
		t.Errorf("provider called %d times while the budget was spent, want 0", n)
	}
}

func TestGatedClient_PassesCallsThroughWhileTheGateAdmits(t *testing.T) {
	cases := map[string]func(*budget.Gate){
		"no state yet": func(*budget.Gate) {},
		"budget not spent": func(g *budget.Gate) {
			g.Apply(context.Background(), budgettest.MeshState(false, gatedPeriodEnd, time.Now()))
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			inner := newCountingLLM()
			gate := budgettest.NewGate()
			prepare(gate)
			client := NewGatedClient(inner, gate)

			resp, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{})
			if err != nil || resp != inner.resp {
				t.Errorf("completion = (%v, %v), want the provider's response", resp, err)
			}
			stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{})
			if err != nil || stream != (<-chan llm.StreamChunk)(inner.stream) {
				t.Errorf("stream = (%v, %v), want the provider's stream", stream, err)
			}
			if n := inner.callCount(); n != 2 {
				t.Errorf("provider called %d times, want 2", n)
			}
		})
	}
}

func TestNewGatedClient_WithoutAGateReturnsTheInnerClient(t *testing.T) {
	inner := newCountingLLM()

	if got := NewGatedClient(inner, nil); got != llm.Client(inner) {
		t.Errorf("got %T, want the inner client itself", got)
	}
}

func TestGatedClient_AdmitsAgainOnceANewerStateLiftsTheBlock(t *testing.T) {
	inner := newCountingLLM()
	gate := budgettest.NewGate()
	blockedAt := time.Now()
	gate.Apply(t.Context(), budgettest.MeshState(true, gatedPeriodEnd, blockedAt))
	client := NewGatedClient(inner, gate)
	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{}); err == nil {
		t.Fatal("completion admitted while blocked")
	}

	gate.Apply(t.Context(), budgettest.MeshState(false, gatedPeriodEnd, blockedAt.Add(time.Minute)))

	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{}); err != nil {
		t.Errorf("completion after the block lifted: %v", err)
	}
	if n := inner.callCount(); n != 1 {
		t.Errorf("provider called %d times, want 1 (only after the block lifted)", n)
	}
}

func TestGatedClient_AnUnconfirmedStateDoesNotStopAnAdmittedTask(t *testing.T) {
	inner := newCountingLLM()
	client := NewGatedClient(inner, budgettest.UnconfirmedGate(t))

	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{}); err != nil {
		t.Errorf("completion refused on an unconfirmed state: %v", err)
	}
}

func TestGatedClient_RefusesOnlyTheUserWhoseOwnBudgetIsSpent(t *testing.T) {
	const spentRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
	inner := newCountingLLM()
	gate := budgettest.NewGate()
	gate.Apply(t.Context(), budgettest.UserBlockedState(gatedPeriodEnd, time.Now(), budget.UserKey(spentRow, ""), budget.UserKey("", "spent-sub")))
	client := NewGatedClient(inner, gate)

	for name, attribution := range map[string]*llm.SpendAttribution{
		"booked to a spent row":           {UserID: spentRow},
		"booked to a spent identity name": {UserSub: "spent-sub"},
	} {
		_, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: attribution})
		exhausted, ok := errors.AsType[*budget.ExhaustedError](err)
		if !ok || exhausted.Tier != budget.TierUser {
			t.Errorf("%s: completion = %v, want the user's own budget refusing", name, err)
		}
		if _, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{Spend: attribution}); err == nil {
			t.Errorf("%s: stream admitted", name)
		}
	}
	if n := inner.callCount(); n != 0 {
		t.Fatalf("provider called %d times for spent users, want 0", n)
	}

	for name, req := range map[string]*llm.ChatRequest{
		"another user":         {Spend: &llm.SpendAttribution{UserSub: "someone-else"}},
		"a call naming nobody": {},
	} {
		if _, err := client.ChatCompletion(t.Context(), req); err != nil {
			t.Errorf("%s: completion = %v, want admitted", name, err)
		}
	}
}
