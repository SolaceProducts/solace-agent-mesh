// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestApplyCacheControl_NoAccumulationAcrossTurns(t *testing.T) {
	const anthropicMaxCacheControlBlocks = 4
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "system", Content: "sys"}},
		Tools:    []llm.ToolDefinition{{Name: "t1"}, {Name: "t2"}},
	}
	count := func() int {
		n := 0
		for _, m := range req.Messages {
			if m.CacheControl != nil {
				n++
			}
		}
		for _, td := range req.Tools {
			if td.CacheControl != nil {
				n++
			}
		}
		return n
	}

	for turn := 1; turn <= 6; turn++ {
		req.Messages = append(req.Messages,
			llm.ChatMessage{Role: "user", Content: fmt.Sprintf("q%d", turn)},
			llm.ChatMessage{Role: "assistant", Content: fmt.Sprintf("a%d", turn)},
		)
		applyCacheControl(req, "5m")
		if n := count(); n > anthropicMaxCacheControlBlocks {
			t.Fatalf("turn %d: %d cache_control breakpoints, exceeds Anthropic's limit of 4", turn, n)
		}
	}
	if n := count(); n != 4 {
		t.Errorf("final breakpoint count = %d, want 4", n)
	}
}

func TestApplyCacheControl_5m(t *testing.T) {
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		Tools: []llm.ToolDefinition{
			{Name: "alpha_tool", Description: "first"},
			{Name: "beta_tool", Description: "second"},
		},
	}

	applyCacheControl(req, "5m")

	if req.Messages[0].CacheControl == nil {
		t.Fatal("system message should have CacheControl set")
	}
	if req.Messages[0].CacheControl.Type != "ephemeral" {
		t.Errorf("system CacheControl.Type = %q, want %q", req.Messages[0].CacheControl.Type, "ephemeral")
	}
	if req.Messages[0].CacheControl.TTL != "" {
		t.Errorf("system CacheControl.TTL = %q, want empty for 5m strategy", req.Messages[0].CacheControl.TTL)
	}

	if req.Messages[1].CacheControl == nil {
		t.Error("last conversation message should have a rolling CacheControl")
	}

	if req.Tools[0].CacheControl != nil {
		t.Error("first tool should not have CacheControl")
	}
	if req.Tools[1].CacheControl == nil {
		t.Fatal("last tool should have CacheControl set")
	}
	if req.Tools[1].CacheControl.Type != "ephemeral" {
		t.Errorf("last tool CacheControl.Type = %q, want %q", req.Tools[1].CacheControl.Type, "ephemeral")
	}
	if req.Tools[1].CacheControl.TTL != "" {
		t.Errorf("last tool CacheControl.TTL = %q, want empty", req.Tools[1].CacheControl.TTL)
	}
}

func TestApplyCacheControl_1h(t *testing.T) {
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		Tools: []llm.ToolDefinition{
			{Name: "tool_a", Description: "only tool"},
		},
	}

	applyCacheControl(req, "1h")

	if req.Messages[0].CacheControl == nil {
		t.Fatal("system message should have CacheControl set")
	}
	if req.Messages[0].CacheControl.TTL != "1h" {
		t.Errorf("system CacheControl.TTL = %q, want %q", req.Messages[0].CacheControl.TTL, "1h")
	}

	if req.Tools[0].CacheControl == nil {
		t.Fatal("tool should have CacheControl set")
	}
	if req.Tools[0].CacheControl.TTL != "1h" {
		t.Errorf("tool CacheControl.TTL = %q, want %q", req.Tools[0].CacheControl.TTL, "1h")
	}
}

func TestApplyCacheControl_NoToolsNoSystemOK(t *testing.T) {
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "user", Content: "Hello"},
		},
	}

	applyCacheControl(req, "5m")

	if req.Messages[0].CacheControl == nil {
		t.Error("the conversation message should get a rolling CacheControl")
	}
}

func TestApplyCacheControl_EmptyRequest(t *testing.T) {
	req := &llm.ChatRequest{}
	applyCacheControl(req, "5m")
}

func TestApplyCacheControl_RollingConversation(t *testing.T) {
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "first question"},
			{Role: "assistant", Content: "first answer"},
			{Role: "user", Content: "second question"},
			{Role: "assistant", Content: "second answer"},
		},
		Tools: []llm.ToolDefinition{
			{Name: "alpha_tool", Description: "first"},
			{Name: "beta_tool", Description: "second"},
		},
	}

	applyCacheControl(req, "5m")

	marked := 0
	for i := range req.Messages {
		if req.Messages[i].CacheControl != nil {
			marked++
		}
	}
	if marked != 3 {
		t.Fatalf("expected 3 marked messages (system + last 2), got %d", marked)
	}
	if req.Messages[0].CacheControl == nil {
		t.Error("system message should be marked")
	}
	if req.Messages[4].CacheControl == nil || req.Messages[3].CacheControl == nil {
		t.Error("the last two messages should be marked (rolling breakpoints)")
	}
	if req.Messages[1].CacheControl != nil || req.Messages[2].CacheControl != nil {
		t.Error("older conversation messages should not be marked")
	}
	if req.Tools[1].CacheControl == nil {
		t.Error("last tool should be marked")
	}

	total := marked
	for i := range req.Tools {
		if req.Tools[i].CacheControl != nil {
			total++
		}
	}
	if total > 4 {
		t.Fatalf("exceeded Anthropic's 4-breakpoint limit: %d", total)
	}
}

func TestApplyCacheControl_SkipsContentlessMessages(t *testing.T) {
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "do something"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "tool", Arguments: "{}"}}},
			{Role: "tool", ToolCallID: "c1", ContentBlocks: []llm.ContentBlock{{Type: "text", Text: "result"}}},
		},
	}

	applyCacheControl(req, "5m")

	if req.Messages[2].CacheControl != nil {
		t.Error("content-less tool-call message should be skipped")
	}
	if req.Messages[1].CacheControl == nil || req.Messages[3].CacheControl == nil {
		t.Error("the two content-bearing messages should be marked")
	}
}

func reqForCacheTest() *llm.ChatRequest {
	return &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		Tools: []llm.ToolDefinition{
			{Name: "alpha_tool", Description: "first"},
			{Name: "beta_tool", Description: "second"},
		},
	}
}

func boundAgentForCacheTest(t *testing.T, raw map[string]any) *Agent {
	t.Helper()
	reg := bindings.New(
		bindings.WithClientBuilder(func(_ context.Context, _ map[string]any, _ *slog.Logger) (llm.Client, error) {
			return &bindingsStubClient{}, nil
		}),
	)
	if err := reg.UpsertModel(t.Context(), "planning", raw); err != nil {
		t.Fatalf("UpsertModel: %v", err)
	}
	agent := New(AgentConfig{Name: "bound-agent", Model: "planning-v1", ModelAlias: "planning"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agent
}

func TestApplyReasoningAndCache_BindingCacheStrategy(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":          "openai/claude-sonnet-4-6",
		"cache_strategy": "1h",
	})
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Messages[0].CacheControl == nil {
		t.Fatal("bound model with cache_strategy should set cache_control on system message")
	}
	if req.Messages[0].CacheControl.TTL != "1h" {
		t.Errorf("system CacheControl.TTL = %q, want 1h", req.Messages[0].CacheControl.TTL)
	}
	if req.Tools[1].CacheControl == nil {
		t.Error("last tool should have cache_control set")
	}
}

func TestApplyReasoningAndCache_BindingDefaults5m(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{"model": "openai/claude-sonnet-4-6"})
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Messages[0].CacheControl == nil {
		t.Fatal("bound model without cache_strategy should default to 5m and set cache_control")
	}
	if req.Messages[0].CacheControl.TTL != "" {
		t.Errorf("system CacheControl.TTL = %q, want empty (5m default)", req.Messages[0].CacheControl.TTL)
	}
}

func TestApplyReasoningAndCache_BindingNone(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":          "openai/claude-sonnet-4-6",
		"cache_strategy": "none",
	})
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Messages[0].CacheControl != nil {
		t.Error("cache_strategy=none should not set cache_control")
	}
}

func TestApplyReasoningAndCache_OverrideWinsOverBinding(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":          "openai/claude-sonnet-4-6",
		"cache_strategy": "none",
	})
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{OverrideCacheStrategy: "1h"})

	if req.Messages[0].CacheControl == nil || req.Messages[0].CacheControl.TTL != "1h" {
		t.Fatalf("per-task override should win over binding; got %+v", req.Messages[0].CacheControl)
	}
}

func TestApplyReasoningAndCache_BindingReasoningBudget(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":    "openai/claude-sonnet-4-6",
		"thinking": map[string]any{"budget_tokens": 2048},
	})
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Reasoning == nil {
		t.Fatal("binding thinking.budget_tokens should populate req.Reasoning")
	}
	if req.Reasoning.BudgetTokens != 2048 {
		t.Errorf("Reasoning.BudgetTokens = %d, want 2048", req.Reasoning.BudgetTokens)
	}
}

func TestApplyReasoningAndCache_NoAliasNoStaticStrategy(t *testing.T) {
	agent := New(AgentConfig{Name: "static-agent", Model: "some-model"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Messages[0].CacheControl != nil {
		t.Error("agent without ModelAlias and empty CacheStrategy should not cache")
	}
}

func TestApplyReasoningAndCache_BindingUnresolved_FallsBack(t *testing.T) {
	reg := newBindingsRegistryForTest(t, nil)
	agent := New(AgentConfig{Name: "bound-agent", Model: "planning-v1", ModelAlias: "planning"})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.bindings = reg
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Messages[0].CacheControl != nil {
		t.Error("unresolved binding should fall back to static config (no caching here)")
	}
	if req.Reasoning != nil {
		t.Error("unresolved binding should not set a reasoning budget")
	}
}

func TestApplyReasoningAndCache_OverrideBudgetBeatsStaticEffort(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":    "openai/claude-sonnet-4-6",
		"thinking": map[string]any{"budget_tokens": 8192},
	})
	agent.cfg.ReasoningEffort = "high"
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{
		OverrideReasoningBudgetTokens: 4096,
	})

	if req.Reasoning == nil {
		t.Fatal("per-task reasoning budget should populate req.Reasoning")
	}
	if req.Reasoning.BudgetTokens != 4096 {
		t.Errorf("Reasoning.BudgetTokens = %d, want 4096", req.Reasoning.BudgetTokens)
	}
	if req.Reasoning.Effort != "" {
		t.Errorf("Reasoning.Effort = %q, want empty; the static effort must not answer for the override tier",
			req.Reasoning.Effort)
	}
}

func TestApplyReasoningAndCache_OverrideEffortBeatsStaticBudget(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":    "openai/claude-sonnet-4-6",
		"thinking": map[string]any{"budget_tokens": 8192},
	})
	agent.cfg.ReasoningBudgetTokens = 2048
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{
		OverrideReasoningEffort: "low",
	})

	if req.Reasoning == nil {
		t.Fatal("per-task reasoning effort should populate req.Reasoning")
	}
	if req.Reasoning.Effort != "low" {
		t.Errorf("Reasoning.Effort = %q, want low", req.Reasoning.Effort)
	}
	if req.Reasoning.BudgetTokens != 0 {
		t.Errorf("Reasoning.BudgetTokens = %d, want 0; a budget sent alongside the effort loses the effort at the provider",
			req.Reasoning.BudgetTokens)
	}
}

func TestApplyReasoningAndCache_OverrideNoneBeatsStaticBudget(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":    "openai/claude-sonnet-4-6",
		"thinking": map[string]any{"budget_tokens": 8192},
	})
	agent.cfg.ReasoningBudgetTokens = 2048
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{
		OverrideReasoningEffort: "none",
	})

	if req.Reasoning == nil {
		t.Fatal("per-task reasoning effort none should populate req.Reasoning")
	}
	if req.Reasoning.Effort != "none" {
		t.Errorf("Reasoning.Effort = %q, want none", req.Reasoning.Effort)
	}
	if req.Reasoning.BudgetTokens != 0 {
		t.Errorf("Reasoning.BudgetTokens = %d, want 0; the static budget must not survive an explicit none",
			req.Reasoning.BudgetTokens)
	}
}

func TestApplyReasoningAndCache_StaticEffortBeatsBindingBudget(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{
		"model":    "openai/claude-sonnet-4-6",
		"thinking": map[string]any{"budget_tokens": 8192},
	})
	agent.cfg.ReasoningEffort = "high"
	req := reqForCacheTest()

	agent.applyReasoningAndCache(t.Context(), req, &TaskExecutionState{})

	if req.Reasoning == nil {
		t.Fatal("the agent's static reasoning effort should populate req.Reasoning")
	}
	if req.Reasoning.Effort != "high" {
		t.Errorf("Reasoning.Effort = %q, want high; the binding must not outrank the agent config",
			req.Reasoning.Effort)
	}
	if req.Reasoning.BudgetTokens != 0 {
		t.Errorf("Reasoning.BudgetTokens = %d, want 0; the binding budget must not answer for the static tier",
			req.Reasoning.BudgetTokens)
	}
}
