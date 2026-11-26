// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type countingUsageLLM struct {
	usageLLM
	calls atomic.Int32
}

func (c *countingUsageLLM) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	c.calls.Add(1)
	return c.usageLLM.ChatCompletion(ctx, req)
}

func (c *countingUsageLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	c.calls.Add(1)
	return c.usageLLM.ChatCompletionStream(ctx, req)
}

func runTaskOnGatedAgent(t *testing.T, model llm.Client, gate *budget.Gate) *a2a.Response {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	svc := runtime.NewRuntime(brk, config.FromMap(nil),
		runtime.WithDefaultLLM(model),
		runtime.WithSessions(session.NewMemoryStore()),
		runtime.WithBudgetGate(gate)).Services()
	exe := NewAWEExecutor(svc)
	if err := exe.LoadConfig(config.FromMap(map[string]any{
		"kind": "agent", "name": "budget-agent",
		config.KeyAppConfig: map[string]any{
			"agent_name": "budget-agent", "namespace": "ns", "model": "test-model", "instruction": "answer",
		},
	})); err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	ag, ok := exe.Instances()[0].(*agent.Agent)
	if !ok {
		t.Fatalf("instance is %T, want an agent", exe.Instances()[0])
	}

	resp, err := ag.HandleTaskToCompletion(t.Context(), idgen.New(), a2a.Message{
		Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser, Parts: []a2a.Part{{Kind: "text", Text: "hi"}},
	}, a2a.UserProperties{UserID: "user-1"})
	if err != nil {
		t.Fatalf("task: %v", err)
	}
	if resp == nil {
		t.Fatal("task returned no response")
	}
	return resp
}

func TestNewAWEExecutor_AnAgentItBuildsRefusesItsLLMCallWhileTheMeshBudgetIsSpent(t *testing.T) {
	model := &countingUsageLLM{}

	resp := runTaskOnGatedAgent(t, model, budgettest.BlockedGate(t, time.Now().Add(24*time.Hour)))

	if n := model.calls.Load(); n != 0 {
		t.Errorf("LLM called %d times while the mesh budget was spent, want 0", n)
	}
	if resp.Error == nil {
		t.Fatalf("task succeeded with result %+v, want a budget refusal", resp.Result)
	}
	data, _ := resp.Error.Data.(map[string]any)
	if got := data[a2a.ErrorDataLLMErrorCategory]; got != taxonomy.ErrorCategoryBudgetExhausted {
		t.Errorf("failure category = %v, want %s", got, taxonomy.ErrorCategoryBudgetExhausted)
	}
}
