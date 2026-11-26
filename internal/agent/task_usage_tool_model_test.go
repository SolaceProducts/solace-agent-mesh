// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func registerPhaseModelTool(t *testing.T, agent *Agent, name string, callWithModel func(toolCtx *tool.Context, model string) error, model string) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:        name,
		description: "calls the LLM on its own model",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
			if err := callWithModel(toolCtx, model); err != nil {
				return nil, err
			}
			return tool.OK("ok"), nil
		},
	})
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
}

func viaInternalLLMCallWithModel(toolCtx *tool.Context, model string) error {
	_, err := toolCtx.InternalLLMCallWithModel(context.Background(), model, "sys", "user", 0.2)
	return err
}

func viaInternalLLMCallMultimodal(toolCtx *tool.Context, model string) error {
	_, err := toolCtx.InternalLLMCallMultimodal(context.Background(), "sys",
		[]tool.MultimodalPart{tool.TextPart{Text: "describe"}},
		tool.InternalLLMCallMultimodalOpts{Model: model})
	return err
}

func runWithPhaseModelTool(t *testing.T, agentModel, model string, callWithModel func(*tool.Context, string) error) map[string]any {
	t.Helper()
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "phase_tool", Arguments: `{}`}}},
		&llm.ChatResponse{Content: "phase answer", FinishReason: "stop"},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.Model = agentModel
	agent.cfg.OriginalModel = agentModel
	registerPhaseModelTool(t, agent, "phase_tool", callWithModel, model)

	resp, err := agent.HandleTask(context.Background(), "task-phase-model",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	return finalTaskOf(t, resp).Metadata
}

func toolModelFamiliesOf(t *testing.T, metadata map[string]any) []string {
	t.Helper()
	raw, present := metadata[a2a.MetadataToolModelFamilies]
	if !present {
		return nil
	}
	families, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a list", a2a.MetadataToolModelFamilies, raw)
	}
	out := make([]string, 0, len(families))
	for _, f := range families {
		s, ok := f.(string)
		if !ok {
			t.Fatalf("%s member %#v is not a string", a2a.MetadataToolModelFamilies, f)
		}
		out = append(out, s)
	}
	return out
}

func TestToolSelectedModelIsReportedBesideTheAgentsOwn(t *testing.T) {
	for _, c := range []struct {
		name string
		call func(*tool.Context, string) error
	}{
		{"InternalLLMCallWithModel", viaInternalLLMCallWithModel},
		{"InternalLLMCallMultimodal", viaInternalLLMCallMultimodal},
	} {
		t.Run(c.name, func(t *testing.T) {
			metadata := runWithPhaseModelTool(t, "anthropic/claude-sonnet-4-5", "gemini-2.5-flash", c.call)

			if got := metadata[a2a.MetadataModelFamily]; got != modelfamily.ClaudeSonnet {
				t.Errorf("%s = %v, want the agent's own %q", a2a.MetadataModelFamily, got, modelfamily.ClaudeSonnet)
			}
			want := []string{modelfamily.GeminiFlash}
			if got := toolModelFamiliesOf(t, metadata); !slices.Equal(got, want) {
				t.Errorf("%s = %v, want %v", a2a.MetadataToolModelFamilies, got, want)
			}
		})
	}
}

func TestAToolOnTheAgentsOwnModelAddsNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		model string
	}{
		{"no model at all", ""},
		{"the agent's own model, named", "anthropic/claude-sonnet-4-5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			metadata := runWithPhaseModelTool(t, "anthropic/claude-sonnet-4-5", c.model, viaInternalLLMCallWithModel)

			if got, present := metadata[a2a.MetadataToolModelFamilies]; present {
				t.Errorf("%s = %v, want nothing reported", a2a.MetadataToolModelFamilies, got)
			}
			if got := metadata[a2a.MetadataModelFamily]; got != modelfamily.ClaudeSonnet {
				t.Errorf("%s = %v, want %q", a2a.MetadataModelFamily, got, modelfamily.ClaudeSonnet)
			}
		})
	}
}

func TestAToolModelOnTheRunsOwnFamilyIsNotReportedTwice(t *testing.T) {
	metadata := runWithPhaseModelTool(t, "claude-sonnet-4-5", "claude-sonnet-4-6", viaInternalLLMCallWithModel)

	if got := metadata[a2a.MetadataModelFamily]; got != modelfamily.ClaudeSonnet {
		t.Fatalf("%s = %v, want %q", a2a.MetadataModelFamily, got, modelfamily.ClaudeSonnet)
	}
	if got, present := metadata[a2a.MetadataToolModelFamilies]; present {
		t.Errorf("%s = %v, want the run's own family dropped from the set", a2a.MetadataToolModelFamilies, got)
	}
}

func TestAToolsModelIdentifierNeverReachesTheTerminalMetadata(t *testing.T) {
	cases := map[string]string{
		"ft:gpt-4o:acme-corp:custom:9xYz": modelfamily.GPT4o,
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/anthropic.claude-sonnet-4-5": modelfamily.ClaudeSonnet,
		"acme-prod-westeurope-deployment": modelfamily.Other,
	}
	for modelID, wantFamily := range cases {
		t.Run(modelID, func(t *testing.T) {
			metadata := runWithPhaseModelTool(t, "gpt-5", modelID, viaInternalLLMCallWithModel)

			rendered, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("marshal metadata: %v", err)
			}
			for _, secret := range []string{
				modelID, "acme-corp", "9xYz", "123456789012", "us-east-1", "westeurope",
			} {
				if strings.Contains(string(rendered), secret) {
					t.Errorf("%q reached the terminal metadata %s", secret, rendered)
				}
			}
			if got := toolModelFamiliesOf(t, metadata); !slices.Equal(got, []string{wantFamily}) {
				t.Errorf("%s = %v, want %v", a2a.MetadataToolModelFamilies, got, []string{wantFamily})
			}
		})
	}
}

func TestToolAndTaskGoroutinesRecordConcurrently(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: state.TaskID, AgentName: "test-agent"}, slog.Default())
	toolCtx := agent.buildToolCtxFromState(t.Context(), state, sp)

	models := []string{"gpt-4o-mini", "gemini-2.5-flash", "llama-3.3-70b-versatile", "mistral-small-latest"}
	var wg sync.WaitGroup
	for _, model := range models {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if _, err := toolCtx.InternalLLMCallWithModel(t.Context(), model, "sys", "user", 0.1); err != nil {
					t.Errorf("InternalLLMCallWithModel(%q): %v", model, err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			state.usageFacts.recordModel("claude-sonnet-4-5", "anthropic", "")
			state.usageFacts.AddTo(map[string]any{})
		}
	}()
	wg.Wait()

	metadata := map[string]any{}
	state.usageFacts.AddTo(metadata)
	want := []string{modelfamily.GPT4o, modelfamily.GeminiFlash, modelfamily.Llama, modelfamily.Mistral}
	slices.Sort(want)
	got, ok := metadata[a2a.MetadataToolModelFamilies].([]string)
	if !ok {
		t.Fatalf("%s = %#v, want a list of families", a2a.MetadataToolModelFamilies, metadata[a2a.MetadataToolModelFamilies])
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolModelFamilies, got, want)
	}
}

func TestToolModelVocabularyMatchesTheCatalog(t *testing.T) {
	for _, modelID := range []string{
		"gpt-4o", "openai/gpt-4.1-2025-04-14", "anthropic.claude-3-5-sonnet-20241022-v2:0",
		"ft:gpt-4o:acme-corp:legal-triage:AbCd1234", "acme-internal-vllm-cluster",
		"gemini-1.5-pro", "llama-3.3-70b-versatile", "mistral-small-latest",
	} {
		var facts taskUsageFacts
		facts.recordToolModel(modelID)
		metadata := map[string]any{}
		facts.AddTo(metadata)
		for _, family := range metadata[a2a.MetadataToolModelFamilies].([]string) {
			if !slices.Contains(modelfamily.Families, family) {
				t.Errorf("recordToolModel(%q) wrote family %q, which the catalog does not approve", modelID, family)
			}
		}
	}
}

func TestToolLLMCallUsageCountsTowardTheTaskTotals(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "phase_tool", Arguments: `{}`}},
			Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}},
		&llm.ChatResponse{Content: "phase answer", FinishReason: "stop",
			Usage: &llm.Usage{PromptTokens: 400, CompletionTokens: 40, CachedTokens: 300, CacheWriteTokens: 50, TotalTokens: 440}},
		&llm.ChatResponse{Content: "done", FinishReason: "stop",
			Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220}},
	)
	agent.cfg.Model = "gpt-4o"
	agent.cfg.OriginalModel = "gpt-4o"
	registerPhaseModelTool(t, agent, "phase_tool", viaInternalLLMCallWithModel, "gemini-2.5-flash")

	resp, err := agent.HandleTask(context.Background(), "task-tool-usage",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	tu, ok := finalTaskOf(t, resp).Metadata["token_usage"].(map[string]any)
	if !ok {
		t.Fatalf("terminal metadata has no token_usage")
	}
	if fmt.Sprint(tu["total_input_tokens"], tu["total_output_tokens"], tu["total_cache_write_tokens"]) != "700 70 50" {
		t.Errorf("token_usage totals = %#v, want input 700 / output 70 / cache write 50", tu)
	}
	byModel, _ := tu["by_model"].(map[string]any)
	for model, wantInput := range map[string]int{"gpt-4o": 300, "gemini-2.5-flash": 400} {
		entry, ok := byModel[model].(map[string]any)
		if !ok {
			t.Fatalf("by_model has no entry for %q: %#v", model, byModel)
		}
		if fmt.Sprint(entry["input_tokens"]) != fmt.Sprint(wantInput) {
			t.Errorf("by_model[%s].input_tokens = %v, want %d", model, entry["input_tokens"], wantInput)
		}
	}
	if kind := byModel["gemini-2.5-flash"].(map[string]any)["kind"]; kind != byModelKindTool {
		t.Errorf("by_model[gemini-2.5-flash].kind = %v, want %q: only the tool used it", kind, byModelKindTool)
	}
	if kind, tagged := byModel["gpt-4o"].(map[string]any)["kind"]; tagged {
		t.Errorf("by_model[gpt-4o].kind = %v, want none: it is the agent's own model", kind)
	}
}

func TestAToolCallOnTheAgentsModelLeavesItsEntryUntagged(t *testing.T) {
	usage := &llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "phase_tool", Arguments: `{}`}}, Usage: usage},
		&llm.ChatResponse{Content: "phase answer", FinishReason: "stop", Usage: usage},
		&llm.ChatResponse{Content: "done", FinishReason: "stop", Usage: usage},
	)
	agent.cfg.Model = "gpt-4o"
	agent.cfg.OriginalModel = "gpt-4o"
	registerPhaseModelTool(t, agent, "phase_tool", viaInternalLLMCallWithModel, "gpt-4o")

	resp, err := agent.HandleTask(context.Background(), "task-tool-own-model",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	tu, _ := finalTaskOf(t, resp).Metadata["token_usage"].(map[string]any)
	byModel, _ := tu["by_model"].(map[string]any)
	entry, ok := byModel["gpt-4o"].(map[string]any)
	if !ok {
		t.Fatalf("by_model has no gpt-4o entry: %#v", byModel)
	}
	if fmt.Sprint(entry["input_tokens"]) != "300" {
		t.Errorf("by_model[gpt-4o].input_tokens = %v, want the agent's and the tool's 300", entry["input_tokens"])
	}
	if kind, tagged := entry["kind"]; tagged {
		t.Errorf("by_model[gpt-4o].kind = %v, want none", kind)
	}
}

func TestParallelToolLLMCallsAllCountTowardTheTaskTotals(t *testing.T) {
	toolCall := func(id string) llm.ToolCall { return llm.ToolCall{ID: id, Name: "phase_tool", Arguments: `{}`} }
	toolAnswer := func() *llm.ChatResponse {
		return &llm.ChatResponse{Content: "phase answer", FinishReason: "stop",
			Usage: &llm.Usage{PromptTokens: 50, CompletionTokens: 5, TotalTokens: 55}}
	}
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{toolCall("call-1"), toolCall("call-2"), toolCall("call-3")},
			Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}},
		toolAnswer(), toolAnswer(), toolAnswer(),
		&llm.ChatResponse{Content: "done", FinishReason: "stop",
			Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220}},
	)
	agent.cfg.Model = "gpt-4o"
	agent.cfg.OriginalModel = "gpt-4o"
	registerPhaseModelTool(t, agent, "phase_tool", viaInternalLLMCallWithModel, "gemini-2.5-flash")

	resp, err := agent.HandleTask(context.Background(), "task-parallel-tool-usage",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	tu, _ := finalTaskOf(t, resp).Metadata["token_usage"].(map[string]any)
	if fmt.Sprint(tu["total_input_tokens"], tu["total_output_tokens"]) != "450 45" {
		t.Errorf("token_usage totals = %#v, want input 450 / output 45", tu)
	}
	byModel, _ := tu["by_model"].(map[string]any)
	if entry, _ := byModel["gemini-2.5-flash"].(map[string]any); fmt.Sprint(entry["input_tokens"]) != "150" {
		t.Errorf("by_model[gemini-2.5-flash] = %#v, want the three tool calls' 150 input tokens", entry)
	}
}

func TestToolLLMUsageRecordedAfterDispatchReachesTheFailureAndCancelTotals(t *testing.T) {
	late := llm.Usage{PromptTokens: 400, CompletionTokens: 40, TotalTokens: 440}

	t.Run("failed", func(t *testing.T) {
		agent, _, _ := newTestAgent()
		state := newTestTaskState(agent)
		state.toolLLMUsage.record("gemini-2.5-flash", late)

		resp := errorResponseWithUsage(state, -32603, "boom")
		data, _ := resp.Error.Data.(map[string]any)
		tu, _ := data["token_usage"].(map[string]any)
		if fmt.Sprint(tu["total_input_tokens"], tu["total_output_tokens"]) != "400 40" {
			t.Errorf("failure token_usage = %#v, want the late tool call's 400 / 40", tu)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		agent, _, _ := newTestAgent()
		state := newTestTaskState(agent)
		state.toolLLMUsage.record("gemini-2.5-flash", late)

		resp := buildCancelledResponse(state.TaskID, state.ContextID, agent.cfg.Name, state.usageSnapshot(), &state.usageFacts)
		tu, _ := finalTaskOf(t, resp).Metadata["token_usage"].(map[string]any)
		if fmt.Sprint(tu["total_input_tokens"], tu["total_output_tokens"]) != "400 40" {
			t.Errorf("cancel token_usage = %#v, want the late tool call's 400 / 40", tu)
		}
	})
}
