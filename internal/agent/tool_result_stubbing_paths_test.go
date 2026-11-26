// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	geminiTestModel = "gemini/gemini-4-neon"
	claudeTestModel = "anthropic/claude-sonnet-5-5"
	smallLookupText = "three rows"
)

func registerSmallLookupTool(t *testing.T, agent *Agent) {
	t.Helper()
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "lookup",
		description: "returns a small result",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			return &tool.Result{Status: tool.StatusSuccess, Message: smallLookupText}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func lookupCall(id string) *llm.ChatResponse {
	return &llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: []llm.ToolCall{{ID: id, Name: "lookup", Arguments: `{}`}}}
}

func replayedToolResults(req llm.ChatRequest) []llm.ChatMessage {
	var results []llm.ChatMessage
	for _, m := range req.Messages {
		if m.Role == "tool" {
			results = append(results, m)
		}
	}
	return results
}

func requireToolResultsStubbed(t *testing.T, req llm.ChatRequest, wantStubbed bool, wantCount int) {
	t.Helper()
	results := replayedToolResults(req)
	if len(results) != wantCount {
		t.Fatalf("request replays %d tool results, want %d: %+v", len(results), wantCount, results)
	}
	for _, m := range results {
		if wantStubbed && m.Content != stubbedToolResult {
			t.Errorf("tool result %s replayed as %q, want the placeholder", m.ToolCallID, m.Content)
		}
		if !wantStubbed && !strings.Contains(m.Content, smallLookupText) {
			t.Errorf("tool result %s replayed as %q, want its raw text %q", m.ToolCallID, m.Content, smallLookupText)
		}
	}
}

func requireTaskSucceeded(t *testing.T, taskID string, resp *a2a.Response, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", taskID, err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("%s: response %+v", taskID, resp)
	}
}

func withModelOverride(msg a2a.Message) a2a.Message {
	msg.Metadata = map[string]any{"model_override": map[string]any{"model_id": "override-alias"}}
	return msg
}

func TestHandleTask_StubModeFollowsTheTaskModelOverride(t *testing.T) {
	tests := []struct {
		name          string
		agentModel    string
		overrideModel string
		wantStubbed   bool
	}{
		{"gemini agent overridden to claude stubs", geminiTestModel, "anthropic/claude-sonnet-4-5", true},
		{"claude agent overridden to gemini keeps small results raw", claudeTestModel, "gemini/gemini-4-flash", false},
		{"opaque config model but gemini report model keeps small results raw", claudeTestModel, "gemini/acme-router", false},
		{"claude agent overridden to an opaque model on the gemini provider keeps small results raw", claudeTestModel, "google/acme-tuned", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent, mock, _ := newTestAgent(
				lookupCall("c1"),
				&llm.ChatResponse{Content: "done", FinishReason: "stop"},
				&llm.ChatResponse{Content: "done again", FinishReason: "stop"},
			)
			agent.cfg.Model = tc.agentModel
			agent.svc.(*mockServices).modelResolver = &stubResolver{resp: map[string]any{"model": tc.overrideModel}}
			registerSmallLookupTool(t, agent)

			props := a2a.UserProperties{ClientID: "u1", UserID: "u1"}
			for _, taskID := range []string{"task-1", "task-2"} {
				resp, err := agent.HandleTask(context.Background(), taskID, withModelOverride(newUserMessage("query")), props)
				requireTaskSucceeded(t, taskID, resp, err)
			}

			calls := mock.getCalls()
			if len(calls) != 3 {
				t.Fatalf("LLM calls = %d, want 3", len(calls))
			}
			requireToolResultsStubbed(t, calls[2], tc.wantStubbed, 1)
		})
	}
}

type overflowOnNthStreamLLM struct {
	*mockLLM
	failOn int32
	seen   atomic.Int32
}

func (c *overflowOnNthStreamLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if c.seen.Add(1) == c.failOn {
		return nil, &llm.Error{StatusCode: 400, Message: "prompt is too long: 213456 tokens > 200000 maximum", Provider: "anthropic"}
	}
	return c.mockLLM.ChatCompletionStream(ctx, req)
}

func TestHandleTask_ContextLimitRetryKeepsTheStubModeOfTheModel(t *testing.T) {
	tests := []struct {
		name          string
		agentModel    string
		overrideModel string
		wantStubbed   bool
	}{
		{"gemini agent keeps its own small result raw", geminiTestModel, "", false},
		{"claude agent stubs its own result", claudeTestModel, "", true},
		{"claude agent overridden to gemini keeps its own small result raw", claudeTestModel, "gemini/gemini-4-flash", false},
		{"agent on the gemini provider under an opaque name keeps its own small result raw", "google/acme-tuned", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const overflowingStream = 4
			client := &overflowOnNthStreamLLM{failOn: overflowingStream, mockLLM: newMockLLM(
				lookupCall("c1"),
				&llm.ChatResponse{Content: "done", FinishReason: "stop"},
				lookupCall("c2"),
				&llm.ChatResponse{Content: "summary", FinishReason: "stop"},
				&llm.ChatResponse{Content: "done again", FinishReason: "stop"},
			)}
			agent := New(AgentConfig{Name: "test-agent", Model: tc.agentModel, Namespace: "test-ns", MaxLLMCalls: 6})
			agent.cfg.AutoSummarization = &CompactionConfig{Enabled: true, CompactionPercentage: 0.5}
			svc := newMockServices(client, newMemSessionStore())
			if tc.overrideModel != "" {
				svc.modelResolver = &stubResolver{resp: map[string]any{"model": tc.overrideModel}}
			}
			agent.svc = svc
			if err := agent.Init(t.Context(), nil, svc); err != nil {
				t.Fatalf("Init: %v", err)
			}
			registerSmallLookupTool(t, agent)

			props := a2a.UserProperties{ClientID: "u1", UserID: "u1"}
			for _, taskID := range []string{"task-1", "task-2"} {
				msg := newUserMessage("query")
				if tc.overrideModel != "" {
					msg = withModelOverride(msg)
				}
				resp, err := agent.HandleTask(t.Context(), taskID, msg, props)
				requireTaskSucceeded(t, taskID, resp, err)
			}

			if got := client.seen.Load(); got <= overflowingStream {
				t.Fatalf("stream calls = %d, so the overflow on call %d never led to a retry", got, overflowingStream)
			}
			calls := client.getCalls()
			retried := calls[len(calls)-1]
			if results := replayedToolResults(retried); len(results) != 1 || results[0].ToolCallID != "c2" {
				t.Fatalf("the retried request replays %+v, want only the task's own result c2: turn 1 is folded into the summary", results)
			}
			requireToolResultsStubbed(t, retried, tc.wantStubbed, 1)
		})
	}
}

func TestAdoptedTaskPromptStubsAnEarlierTurnsSmallResultByModel(t *testing.T) {
	tests := []struct {
		name             string
		agentModel       string
		overrideProvider string
		overrideReport   string
		wantStubbed      bool
	}{
		{"gemini agent keeps the earlier small result raw", geminiTestModel, "", "", false},
		{"claude agent stubs the earlier result", claudeTestModel, "", "", true},
		{"claude agent with a gemini task override keeps the earlier small result raw", claudeTestModel, "openai", "gemini/gemini-4-flash", false},
		{"claude agent with an opaque override on the gemini provider keeps the earlier small result raw", claudeTestModel, "gemini", "google/acme-router", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			agent, _, _ := replicaOf(t, store, "adopter")
			agent.cfg.Model = tc.agentModel
			state := pausedOnPeerCall(t, store, "task-adopted", "sub-adopted")
			earlier := []session.Message{
				{Role: "user", Content: "look it up", TaskID: "task-earlier"},
				{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "e1", Name: "lookup", Arguments: "{}"}}, TaskID: "task-earlier"},
				{Role: "tool", Content: smallLookupText, ToolCallResult: session.ToolCallResult{ToolCallID: "e1", ToolName: "lookup"}, TaskID: "task-earlier"},
				{Role: "assistant", Content: "found them", TaskID: "task-earlier"},
			}
			if err := store.AppendMessages(t.Context(), "test-agent", state.UserProps.UserID, state.EffectiveSessionID, earlier); err != nil {
				t.Fatalf("store earlier turn: %v", err)
			}
			storeOwnRows(t, store, state,
				session.Message{Role: "user", Content: "delegate this"},
				session.Message{Role: "assistant", ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
			)
			adopted, rc := adoptedForRebuild(t, agent, store, state)
			if tc.overrideReport != "" {
				adopted.ModelOverride = &llm.BifrostConfig{Provider: tc.overrideProvider, Model: "acme-router"}
				adopted.ModelOverrideReportModel = tc.overrideReport
			}

			agent.rebuildAdoptedPrompt(t.Context(), adopted, rc)

			prompt := llm.ChatRequest{Messages: adopted.Messages}
			if results := replayedToolResults(prompt); len(results) != 1 || results[0].ToolCallID != "e1" {
				t.Fatalf("the adopted prompt replays %+v, want only the earlier turn's e1", results)
			}
			requireToolResultsStubbed(t, prompt, tc.wantStubbed, 1)
		})
	}
}

func TestHandleTask_ReplaysAnEarlierTurnsSmallResultRawOnlyForGemini(t *testing.T) {
	for model, wantStubbed := range map[string]bool{geminiTestModel: false, claudeTestModel: true} {
		t.Run(model, func(t *testing.T) {
			agent, mock, _ := newTestAgent(
				lookupCall("c1"),
				&llm.ChatResponse{Content: "done", FinishReason: "stop"},
				&llm.ChatResponse{Content: "done again", FinishReason: "stop"},
			)
			agent.cfg.Model = model
			registerSmallLookupTool(t, agent)

			props := a2a.UserProperties{ClientID: "u1", UserID: "u1"}
			for _, taskID := range []string{"task-1", "task-2"} {
				resp, err := agent.HandleTask(context.Background(), taskID, newUserMessage("query"), props)
				requireTaskSucceeded(t, taskID, resp, err)
			}

			calls := mock.getCalls()
			if len(calls) != 3 {
				t.Fatalf("LLM calls = %d, want 3", len(calls))
			}
			requireToolResultsStubbed(t, calls[2], wantStubbed, 1)
		})
	}
}

func TestAgentStubBatchBytesFollowsTheAgentsConfiguredModel(t *testing.T) {
	geminiBatch := llm.ToolResultStubBatchBytes("gemini", "")
	tests := []struct {
		configured string
		bare       string
		want       int
	}{
		{"google/acme-tuned", "acme-tuned", geminiBatch},
		{"openai/gemini-3-flash-preview", "gemini-3-flash-preview", geminiBatch},
		{"vertex_ai/gemini-3.6-flash", "gemini-3.6-flash", geminiBatch},
		{"anthropic/claude-sonnet-5-5", "claude-sonnet-5-5", 0},
		{"openai/acme-router", "acme-router", 0},
	}
	for _, tc := range tests {
		agent := New(AgentConfig{Name: "test-agent", Model: tc.bare, OriginalModel: tc.configured})
		if got := agent.taskStubBatchBytes(t.Context(), nil); got != tc.want {
			t.Errorf("agent configured with %q: batch %d bytes, want %d", tc.configured, got, tc.want)
		}
	}
}

func TestAgentStubBatchBytesFollowsTheModelBoundToItsAlias(t *testing.T) {
	geminiBatch := llm.ToolResultStubBatchBytes("gemini", "")
	reg := newBindingsRegistryForTest(t, nil)
	for alias, model := range map[string]string{"gemini-alias": "google/acme-tuned", "claude-alias": claudeTestModel} {
		if err := reg.UpsertModel(t.Context(), alias, map[string]any{"id": alias + "-v1", "model": model}); err != nil {
			t.Fatalf("UpsertModel %s: %v", alias, err)
		}
	}
	bound := unreadableBindings{reg, map[string]error{"down-alias": errors.New("platform unreachable")}}
	for alias, want := range map[string]int{"gemini-alias": geminiBatch, "claude-alias": 0, "down-alias": 0} {
		t.Run(alias, func(t *testing.T) {
			agent := New(AgentConfig{Name: "test-agent", ModelAlias: alias})
			svc := newMockServices(newMockLLM(), newMemSessionStore())
			svc.bindings = bound
			if err := agent.Init(t.Context(), nil, svc); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if got := agent.taskStubBatchBytes(t.Context(), nil); got != want {
				t.Errorf("agent bound to %s: batch %d bytes, want %d", alias, got, want)
			}
		})
	}
}
