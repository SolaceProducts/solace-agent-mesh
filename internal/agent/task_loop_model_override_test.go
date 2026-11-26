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
)

func llmSignalModels(t *testing.T, msgs []*broker.Message) map[string][]string {
	t.Helper()
	models := map[string][]string{}
	for _, msg := range msgs {
		var envelope struct {
			Result struct {
				Status struct {
					Message *struct {
						Parts []struct {
							Kind string         `json:"kind"`
							Data map[string]any `json:"data"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if err := json.Unmarshal(msg.Payload, &envelope); err != nil || envelope.Result.Status.Message == nil {
			continue
		}
		for _, part := range envelope.Result.Status.Message.Parts {
			var holder map[string]any
			switch part.Data["type"] {
			case string(a2a.SignalLLMInvocation):
				holder, _ = part.Data["request"].(map[string]any)
			case string(a2a.SignalLLMResponse):
				holder, _ = part.Data["usage"].(map[string]any)
			default:
				continue
			}
			model, _ := holder["model"].(string)
			kind := part.Data["type"].(string)
			models[kind] = append(models[kind], model)
		}
	}
	return models
}

func TestHandleTask_ModelOverride_ReportsTheServingModel(t *testing.T) {
	const taskID, served = "task-mo-reported", "anthropic/claude-sonnet-4-5"
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{
			ToolCalls:    []llm.ToolCall{{ID: "tc-1", Name: "_continue_generation", Arguments: "{}"}},
			FinishReason: "tool_calls",
			Usage:        &llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110},
		},
		&llm.ChatResponse{
			Content:      "answer",
			FinishReason: "stop",
			Usage:        &llm.Usage{PromptTokens: 150, CompletionTokens: 20, TotalTokens: 170},
		},
	)
	agent.cfg.Model, agent.cfg.OriginalModel = "gpt-4o", "gpt-4o"
	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{"model": served, "max_input_tokens": 123_456}}

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	ms.brk = brk
	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/" + taskID
	q, err := brk.CreateQueue("reported-"+taskID, broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, statusTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	msg := newUserMessage("please use override")
	msg.Metadata = map[string]any{"model_override": map[string]any{"model_id": "sonnet-alias"}}
	resp, err := agent.HandleTask(t.Context(), taskID, msg,
		a2a.UserProperties{ClientID: "c", UserID: "u", A2AStatusTopic: statusTopic})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	models := llmSignalModels(t, receiveAll(t, brk, q, 64, 2*time.Second))
	for _, kind := range []a2a.SignalType{a2a.SignalLLMInvocation, a2a.SignalLLMResponse} {
		if len(models[string(kind)]) == 0 {
			t.Fatalf("no %s signal was published: %v", kind, models)
		}
		for i, m := range models[string(kind)] {
			if m != served {
				t.Errorf("%s %d names %q, want the override's %q", kind, i, m, served)
			}
		}
	}

	byModel, _ := taskTokenUsage(t, resp)["by_model"].(map[string]any)
	if len(byModel) != 1 {
		t.Fatalf("by_model = %#v, want exactly the override's entry", byModel)
	}
	entry, ok := byModel[served].(map[string]any)
	if !ok {
		t.Fatalf("by_model has no %q entry: %#v", served, byModel)
	}
	for key, want := range map[string]float64{"input_tokens": 250, "output_tokens": 30, "max_input_tokens": 123_456} {
		if got, _ := readIntValue(entry[key]); float64(got) != want {
			t.Errorf("by_model[%q].%s = %v, want %v", served, key, entry[key], want)
		}
	}
}

func TestHandleTask_ModelOverride_FlagOn_AppliesOverrideAcrossTurns(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			ToolCalls: []llm.ToolCall{{
				ID: "tc-1", Name: "_continue_generation", Arguments: "{}",
			}},
			FinishReason: "tool_calls",
		},
		&llm.ChatResponse{
			Content:      "answer",
			FinishReason: "stop",
		},
	)

	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{
		"model": "openai/gpt-4o",
	}}

	msg := newUserMessage("please use override")
	msg.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": "gpt4o-alias"},
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-mo-on",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected >=2 LLM calls (tool + follow-up), got %d", len(calls))
	}
	for i, c := range calls {
		if c.ModelOverride == nil {
			t.Errorf("call %d: expected ModelOverride, got nil", i)
			continue
		}
		if c.ModelOverride.Model != "gpt-4o" {
			t.Errorf("call %d: override model = %q, want gpt-4o", i, c.ModelOverride.Model)
		}
	}

	if ms.modelResolver.(*stubResolver).callCount.Load() != 1 {
		t.Errorf("resolver expected 1 call, got %d",
			ms.modelResolver.(*stubResolver).callCount.Load())
	}
}

func TestHandleTask_ModelOverride_UnknownAlias_ReturnsInvalidRequestError(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "should not run"})

	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: nil}

	msg := newUserMessage("boom")
	msg.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": "missing-alias"},
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-mo-404",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error response, got success")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("error code = %d, want -32600", resp.Error.Code)
	}
	if len(mock.getCalls()) != 0 {
		t.Error("LLM must not be invoked when override resolution fails")
	}
}

func TestHandleTask_ModelOverride_FlagOn_MalformedOverride_UsesDefault(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{
		Content:      "ok",
		FinishReason: "stop",
	})

	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{"model": "openai/gpt-4o"}}

	msg := newUserMessage("hi")
	msg.Metadata = map[string]any{
		"model_override": "totally-wrong-shape",
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-mo-malformed",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	calls := mock.getCalls()
	if len(calls) != 1 || calls[0].ModelOverride != nil {
		t.Errorf("expected no override on LLM call, got %+v", calls)
	}
	if ms.modelResolver.(*stubResolver).callCount.Load() != 0 {
		t.Error("resolver must not be called for malformed override")
	}
	if _, still := msg.Metadata["model_override"]; still {
		t.Error("malformed override must be stripped")
	}
}

func TestHandleTask_ModelOverride_IsolationBetweenTasks(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{Content: "a"},
		&llm.ChatResponse{Content: "b"},
	)

	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{
		"model": "anthropic/claude-sonnet-4",
	}}

	msgA := newUserMessage("task A")
	msgA.ContextID = "ctx-A"
	msgA.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": "claude-alias"},
	}
	if _, err := agent.HandleTask(
		context.Background(),
		"task-A",
		msgA,
		a2a.UserProperties{ClientID: "u1", UserID: "userA"},
	); err != nil {
		t.Fatalf("task A: %v", err)
	}

	msgB := newUserMessage("task B")
	msgB.ContextID = "ctx-B"
	if _, err := agent.HandleTask(
		context.Background(),
		"task-B",
		msgB,
		a2a.UserProperties{ClientID: "u1", UserID: "userB"},
	); err != nil {
		t.Fatalf("task B: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	if calls[0].ModelOverride == nil || calls[0].ModelOverride.Model != "claude-sonnet-4" {
		t.Errorf("task A: override missing/wrong, got %+v", calls[0].ModelOverride)
	}
	if calls[1].ModelOverride != nil {
		t.Errorf("task B: override leaked from task A, got %+v", calls[1].ModelOverride)
	}
}

func TestHandleTask_ModelOverride_OwnAliasRegression(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{
		Content:      "own-alias ok",
		FinishReason: "stop",
	})

	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{
		"model":   "openai/gpt-4o",
		"api_key": "sk-own-alias",
	}}

	msg := newUserMessage("use my own alias as override")
	msg.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": agent.cfg.Model},
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-own-alias",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}
	if calls[0].ModelOverride == nil {
		t.Fatal("expected ModelOverride to be set even when alias matches agent's own model_id")
	}
	if calls[0].ModelOverride.Model != "gpt-4o" {
		t.Errorf("ModelOverride.Model = %q, want gpt-4o", calls[0].ModelOverride.Model)
	}
	if ms.modelResolver.(*stubResolver).callCount.Load() != 1 {
		t.Errorf("resolver call count = %d, want 1 (own-alias must still resolve)",
			ms.modelResolver.(*stubResolver).callCount.Load())
	}
}

func TestHandleTask_ModelOverride_HonorsCacheStrategy(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{
		Content:      "ok",
		FinishReason: "stop",
	})

	agent.cfg.CacheStrategy = ""
	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{
		"model":          "openai/gpt-4o",
		"cache_strategy": "1h",
	}}

	msg := newUserMessage("with cache override")
	msg.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": "gpt4o-1h"},
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-cache-override",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}
	if !hasCacheTTL(calls[0], "1h") {
		t.Errorf("expected at least one CacheControl with TTL=1h on the "+
			"override-cache-strategy request, got req=%+v", calls[0])
	}
}

func TestHandleTask_ModelOverride_HonorsReasoningBudget(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{
		Content:      "ok",
		FinishReason: "stop",
	})

	agent.cfg.ReasoningBudgetTokens = 0
	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{
		"model": "anthropic/claude-sonnet-4",
		"thinking": map[string]any{
			"budget_tokens": 50000,
		},
	}}

	msg := newUserMessage("with thinking override")
	msg.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": "claude-thinking"},
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-thinking-override",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}
	if calls[0].Reasoning == nil {
		t.Fatal("Reasoning must be non-nil when override supplies thinking.budget_tokens")
	}
	if calls[0].Reasoning.BudgetTokens != 50000 {
		t.Errorf("Reasoning.BudgetTokens = %d, want 50000",
			calls[0].Reasoning.BudgetTokens)
	}
}

func TestHandleTask_ModelOverride_FallsBackToAgentDefaults(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{
		Content:      "ok",
		FinishReason: "stop",
	})

	agent.cfg.CacheStrategy = "5m"
	agent.cfg.ReasoningBudgetTokens = 1234

	ms := agent.svc.(*mockServices)
	ms.modelResolver = &stubResolver{resp: map[string]any{
		"model":   "openai/gpt-4o",
		"api_key": "sk-fallback",
	}}

	msg := newUserMessage("minimal override")
	msg.Metadata = map[string]any{
		"model_override": map[string]any{"model_id": "min-alias"},
	}

	resp, err := agent.HandleTask(
		context.Background(),
		"task-fallback",
		msg,
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}
	if calls[0].Reasoning == nil || calls[0].Reasoning.BudgetTokens != 1234 {
		t.Errorf("expected reasoning budget to fall back to 1234, got %+v",
			calls[0].Reasoning)
	}
	if !hasCacheTTL(calls[0], "") {
		t.Errorf("expected 5m cache control marker on request (fallback), got %+v",
			calls[0])
	}
}

func hasCacheTTL(req llm.ChatRequest, ttl string) bool {
	for _, m := range req.Messages {
		if m.CacheControl != nil && m.CacheControl.TTL == ttl {
			return true
		}
	}
	for _, td := range req.Tools {
		if td.CacheControl != nil && td.CacheControl.TTL == ttl {
			return true
		}
	}
	return false
}
