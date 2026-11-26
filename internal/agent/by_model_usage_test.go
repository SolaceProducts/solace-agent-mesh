// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestBuildByModelUsage_StaticModelinfoFallback(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Model = "gpt-4o"
	a.cfg.OriginalModel = "gpt-4o"

	state := &TaskExecutionState{TotalUsage: llm.Usage{PromptTokens: 100, CompletionTokens: 25, CachedTokens: 5, ReasoningTokens: 8}}
	got := a.buildByModelUsage(context.Background(), state)

	entry, ok := got["gpt-4o"].(map[string]any)
	if !ok {
		t.Fatalf("expected key 'gpt-4o' in by_model, got %#v", got)
	}
	if entry["input_tokens"] != 100 {
		t.Errorf("input_tokens = %v, want 100", entry["input_tokens"])
	}
	if entry["output_tokens"] != 25 {
		t.Errorf("output_tokens = %v, want 25", entry["output_tokens"])
	}
	if entry["cached_input_tokens"] != 5 {
		t.Errorf("cached_input_tokens = %v, want 5", entry["cached_input_tokens"])
	}
	if entry["reasoning_tokens"] != 8 {
		t.Errorf("reasoning_tokens = %v, want 8", entry["reasoning_tokens"])
	}
	if entry["max_input_tokens"] != 128_000 {
		t.Errorf("max_input_tokens = %v, want 128000 (gpt-4o modelinfo entry)", entry["max_input_tokens"])
	}
}

func TestBuildByModelUsage_UnknownModelOmitsMaxInput(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Model = "totally-unknown-model"
	a.cfg.OriginalModel = "totally-unknown-model"

	state := &TaskExecutionState{TotalUsage: llm.Usage{PromptTokens: 50, CompletionTokens: 10}}
	got := a.buildByModelUsage(context.Background(), state)

	entry, ok := got["totally-unknown-model"].(map[string]any)
	if !ok {
		t.Fatalf("expected key 'totally-unknown-model' in by_model, got %#v", got)
	}
	if _, present := entry["max_input_tokens"]; present {
		t.Errorf("max_input_tokens should be absent for unknown model, got %v", entry["max_input_tokens"])
	}
}

func TestBuildByModelUsage_SplitsByServingModel(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Model = "gpt-4o"
	a.cfg.OriginalModel = "gpt-4o"

	state := &TaskExecutionState{}
	for _, call := range []struct {
		model string
		usage llm.Usage
	}{
		{"gpt-4o", llm.Usage{PromptTokens: 100, CompletionTokens: 10}},
		{"anthropic/claude-sonnet-4-5", llm.Usage{PromptTokens: 300, CompletionTokens: 40, CacheWriteTokens: 7}},
		{"gpt-4o", llm.Usage{PromptTokens: 20, CompletionTokens: 2}},
	} {
		state.recordCallUsage(call.model, call.usage)
	}
	state.recordUnattributedUsage(llm.Usage{PromptTokens: 5, CompletionTokens: 1})

	got := a.buildByModelUsage(context.Background(), state)

	if len(got) != 2 {
		t.Fatalf("by_model = %#v, want entries for exactly the two serving models", got)
	}
	for model, want := range map[string][3]int{
		"gpt-4o":                      {125, 13, 0},
		"anthropic/claude-sonnet-4-5": {300, 40, 7},
	} {
		entry, ok := got[model].(map[string]any)
		if !ok {
			t.Fatalf("by_model has no entry for %q: %#v", model, got)
		}
		if entry["input_tokens"] != want[0] || entry["output_tokens"] != want[1] || entry["cache_write_tokens"] != want[2] {
			t.Errorf("%s = in %v out %v cacheWrite %v, want %v", model,
				entry["input_tokens"], entry["output_tokens"], entry["cache_write_tokens"], want)
		}
	}
}

func TestBuildByModelUsage_EachEntryCarriesItsOwnModelsWindow(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{"model": "openai/gpt-4o", "max_input_tokens": 1_000_000})

	state := &TaskExecutionState{
		ModelOverride:               &llm.BifrostConfig{Provider: "anthropic", Model: "claude-sonnet-4-5"},
		ModelOverrideReportModel:    "anthropic/claude-sonnet-4-5",
		ModelOverrideMaxInputTokens: 150_000,
	}
	state.recordCallUsage("anthropic/claude-sonnet-4-5", llm.Usage{PromptTokens: 300, CompletionTokens: 30})
	state.recordUnattributedUsage(llm.Usage{PromptTokens: 5, CompletionTokens: 1})

	got := agent.buildByModelUsage(t.Context(), state)

	for model, want := range map[string]int{"anthropic/claude-sonnet-4-5": 150_000, agent.reportModel(): 1_000_000} {
		entry, ok := got[model].(map[string]any)
		if !ok {
			t.Fatalf("by_model has no entry for %q: %#v", model, got)
		}
		if entry["max_input_tokens"] != want {
			t.Errorf("%s max_input_tokens = %v, want %d", model, entry["max_input_tokens"], want)
		}
	}
}

func TestCallReportModel(t *testing.T) {
	a, _, _ := newTestAgent()
	a.cfg.Model = "gpt-4o"
	a.cfg.OriginalModel = "gpt-4o"
	ctx := context.Background()

	if got := a.callReportModel(ctx, &TaskExecutionState{}); got != "gpt-4o" {
		t.Errorf("no override: got %q, want the agent's model", got)
	}
	overridden := &TaskExecutionState{
		ModelOverride:            &llm.BifrostConfig{Provider: "anthropic", Model: "claude-sonnet-4-5"},
		ModelOverrideReportModel: "anthropic/claude-sonnet-4-5",
	}
	if got := a.callReportModel(ctx, overridden); got != "anthropic/claude-sonnet-4-5" {
		t.Errorf("override: got %q, want the binding's model string", got)
	}
	overridden.ModelOverrideReportModel = ""
	if got := a.callReportModel(ctx, overridden); got != "claude-sonnet-4-5" {
		t.Errorf("override without a report model: got %q, want ModelOverride.Model", got)
	}
}

func TestTaskModelOverrideReportsTheBindingsModelString(t *testing.T) {
	agent := boundAgentForCacheTest(t, map[string]any{"model": "anthropic/claude-sonnet-4-5"})

	o, reason := agent.resolveTaskModelOverride(t.Context(), map[string]any{
		"model_override": map[string]any{"model_id": "planning"},
	}, slog.New(slog.DiscardHandler))
	if o == nil {
		t.Fatalf("override not resolved: %q", reason)
	}
	if o.ReportModel != "anthropic/claude-sonnet-4-5" || o.Config.Model != "claude-sonnet-4-5" {
		t.Errorf("ReportModel %q, Config.Model %q; want the prefixed and the bare form", o.ReportModel, o.Config.Model)
	}

	var state TaskExecutionState
	state.setModelOverride(o)
	if got := agent.callReportModel(t.Context(), &state); got != "anthropic/claude-sonnet-4-5" {
		t.Errorf("task on the override reports %q, want the binding's model string", got)
	}
}

func TestReadIntValue(t *testing.T) {
	cases := []struct {
		name   string
		input  any
		want   int
		wantOK bool
	}{
		{name: "float64 (JSON-decoded)", input: float64(200_000), want: 200_000, wantOK: true},
		{name: "int", input: 100, want: 100, wantOK: true},
		{name: "int64", input: int64(42), want: 42, wantOK: true},
		{name: "nil", input: nil, want: 0, wantOK: false},
		{name: "string is rejected", input: "200000", want: 0, wantOK: false},
		{name: "bool is rejected", input: true, want: 0, wantOK: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := readIntValue(tt.input)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("readIntValue(%v) = (%d, %v), want (%d, %v)",
					tt.input, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
