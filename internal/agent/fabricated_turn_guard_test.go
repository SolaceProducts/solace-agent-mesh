// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const claudeModel = "anthropic/claude-sonnet-4-6"

func hostNotifyHistory() []llm.ChatMessage {
	return []llm.ChatMessage{
		{Role: "user", Content: "save my notes"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: hostNotifyCallIDPrefix + "1", Name: "_notify_artifact_save"}}},
		{Role: "tool", ToolName: "_notify_artifact_save", ToolCallID: hostNotifyCallIDPrefix + "1", Content: "{}"},
	}
}

func guardedStops(t *testing.T, agent *Agent, state *TaskExecutionState, history []llm.ChatMessage) ([]string, bool) {
	t.Helper()
	req := &llm.ChatRequest{}
	applied := agent.guardAgainstFabricatedTurn(t.Context(), state, req, history)
	return req.StopSequences, applied
}

func TestGuardAgainstFabricatedTurn(t *testing.T) {
	notifyTail := hostNotifyHistory()
	tests := []struct {
		name    string
		model   string
		history []llm.ChatMessage
		guarded bool
	}{
		{"claude turn after the host notify", claudeModel, notifyTail, true},
		{"bedrock claude id", "bedrock/anthropic.claude-sonnet-4-6", notifyTail, true},
		{"gateway alias made of a tier alone", "openai/sonnet-prod", notifyTail, true},
		{"claude id without a tier", "claude-2.1", notifyTail, true},
		{"claude turn after an ordinary tool result", claudeModel, []llm.ChatMessage{
			{Role: "tool", ToolName: "web_search", ToolCallID: "call-1"},
		}, false},
		{"claude turn after a notify the model invented", claudeModel, []llm.ChatMessage{
			{Role: "tool", ToolName: "_notify_artifact_save", ToolCallID: "call-1"},
		}, false},
		{"claude turn after a rejected call's corrective result", claudeModel, append(slices.Clone(notifyTail),
			llm.ChatMessage{Role: "tool", ToolName: "unknown_tool", ToolCallID: "call-2"}), false},
		{"claude opening turn", claudeModel, []llm.ChatMessage{{Role: "user", Content: "hi"}}, false},
		{"claude with no history", claudeModel, nil, false},
		{"openai reasoning model rejects stop", "openai/gpt-5", notifyTail, false},
		{"gemini", "gemini/gemini-2.5-pro", notifyTail, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.cfg.Model = tc.model

			stops, applied := guardedStops(t, agent, &TaskExecutionState{}, tc.history)

			if applied != tc.guarded {
				t.Fatalf("applied = %v, want %v", applied, tc.guarded)
			}
			if tc.guarded && !slices.Equal(stops, fabricatedTurnStops) {
				t.Fatalf("StopSequences = %q, want %q", stops, fabricatedTurnStops)
			}
			if !tc.guarded && len(stops) != 0 {
				t.Fatalf("StopSequences = %q, want none", stops)
			}
		})
	}
}

func TestGuardJudgesTheModelTheCallRunsUnder(t *testing.T) {
	overrideTo := func(reportModel string) *TaskExecutionState {
		return &TaskExecutionState{
			ModelOverride:            &llm.BifrostConfig{Model: reportModel},
			ModelOverrideReportModel: reportModel,
		}
	}

	t.Run("a Claude override over a non-Claude agent model is guarded", func(t *testing.T) {
		agent, _, _ := newTestAgent()
		agent.cfg.Model = "openai/gpt-5"

		if _, applied := guardedStops(t, agent, overrideTo(claudeModel), hostNotifyHistory()); !applied {
			t.Error("the override runs the call, so its model decides")
		}
	})
	t.Run("a non-Claude override over a Claude agent model is not guarded", func(t *testing.T) {
		agent, _, _ := newTestAgent()
		agent.cfg.Model = claudeModel

		if stops, applied := guardedStops(t, agent, overrideTo("openai/gpt-5"), hostNotifyHistory()); applied {
			t.Errorf("the override runs the call, so its model decides; got %q", stops)
		}
	})
	t.Run("an alias-bound agent is judged by the model its alias resolves to", func(t *testing.T) {
		agent := boundAgentForCacheTest(t, map[string]any{"model": claudeModel})
		agent.cfg.Model = ""

		if _, applied := guardedStops(t, agent, &TaskExecutionState{}, hostNotifyHistory()); !applied {
			t.Error("an agent whose config names no model must be judged by its resolved alias")
		}
	})
}

func TestFabricatedTurnStopsAreTheseTwoMarkers(t *testing.T) {
	for _, stop := range []string{"\n\nHuman:", "[Current time: "} {
		if !slices.Contains(fabricatedTurnStops, stop) {
			t.Errorf("fabricatedTurnStops = %q, missing %q", fabricatedTurnStops, stop)
		}
	}
}

func TestFabricatedTurnIsCutBeforeAnyOfItReachesTheUser(t *testing.T) {
	const kept = "Saved the file."
	for name, generated := range map[string]string{
		"role marker then stamp": "\n\nHuman: [Current time: Monday, 2030-01-07T10:00:00Z (UTC)]\nplease rename the second column",
		"stamp alone":            "\n\n[Current time: Monday, 2030-01-07T10:00:00Z (UTC)]\nplease add a footer",
	} {
		t.Run(name, func(t *testing.T) {
			text := kept + generated
			cut := len(text)
			for _, stop := range fabricatedTurnStops {
				if i := strings.Index(text, stop); i >= 0 {
					cut = min(cut, i)
				}
			}

			shown := text[:cut]

			if strings.Contains(shown, "Human") || strings.Contains(shown, "Current time") || strings.Contains(shown, "please") {
				t.Errorf("text shown to the user before the first stop still carries the fabricated turn: %q", shown)
			}
		})
	}
}

func newArtifactSavingTestAgent(t *testing.T, responses ...*llm.ChatResponse) (*Agent, *mockLLM) {
	t.Helper()
	agent, mock, store := newTestAgent(responses...)
	agent.cfg.Model = claudeModel
	svc := newMockServices(agent.svc.LLM(""), store)
	svc.brk = newMockBroker()
	svc.arts = &artifact.ScopedService{Inner: memoryArtifactStore(), Scope: "namespace", Namespace: "test-ns"}
	agent.svc = svc
	return agent, mock
}

func TestTaskLoopStopsFabricatedTurnOnlyAfterNotify(t *testing.T) {
	savedNotes := BlockPrefixSaveArtifact + " filename=\"notes.md\" description=\"Notes\"\nSome notes.\n" + BlockDelimiterClose

	for name, metadata := range map[string]map[string]any{
		"plain request":                nil,
		"request with gateway context": {metaConversationContext: "earlier in this thread"},
	} {
		t.Run(name, func(t *testing.T) {
			agent, mock := newArtifactSavingTestAgent(t,
				&llm.ChatResponse{Content: savedNotes, FinishReason: "stop"},
				&llm.ChatResponse{Content: "", FinishReason: "stop"},
			)
			msg := newUserMessage("save my notes")
			msg.Metadata = metadata

			resp, err := agent.HandleTask(t.Context(), "task-fabricated-turn", msg,
				a2a.UserProperties{ClientID: "user-1", UserID: "user-1", A2AStatusTopic: "test-ns/a2a/v1/agent/status/test-agent"})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Error != nil {
				t.Fatalf("an empty reply after the notify turn must end the task cleanly, got %+v", resp.Error)
			}

			calls := mock.getCalls()
			if len(calls) != 2 {
				t.Fatalf("expected the answer turn and the notify turn, got %d LLM calls", len(calls))
			}
			if len(calls[0].StopSequences) != 0 {
				t.Errorf("the answer turn must not carry stop sequences, got %q", calls[0].StopSequences)
			}
			if !slices.Equal(calls[1].StopSequences, fabricatedTurnStops) {
				t.Errorf("the notify turn StopSequences = %q, want %q", calls[1].StopSequences, fabricatedTurnStops)
			}
		})
	}
}

func TestStructuredRetryFollowUpAfterNotifyCarriesTheStops(t *testing.T) {
	savedNotes := BlockPrefixSaveArtifact + " filename=\"notes.md\" description=\"Notes\"\nSome notes.\n" + BlockDelimiterClose + "\nSaved your notes."
	agent, mock := newArtifactSavingTestAgent(t,
		&llm.ChatResponse{Content: savedNotes, FinishReason: "stop"},
		&llm.ChatResponse{Content: "", FinishReason: "stop"},
		&llm.ChatResponse{Content: "", FinishReason: "stop"},
	)
	handler, err := structured.NewHandler(structured.HandlerConfig{MaxRetries: 1, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	agent.structuredHandler = handler

	state := &TaskExecutionState{
		TaskID:    "task-structured-notify",
		Messages:  []llm.ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "produce the report"}},
		UserProps: a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	}
	rc := &taskRunContext{
		sp:  NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-structured-notify", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default()),
		msg: newUserMessage("produce the report"),
		structuredCtx: &structured.Context{
			Request:    &a2a.StructuredInvocationRequest{NodeID: "node-1"},
			MaxRetries: 1,
		},
		toolCtx:           &tool.Context{},
		originalSessionID: "sess-1",
		logger:            agent.logger,
	}

	if _, err := agent.finalizeStructuredInvocation(t.Context(), state, rc, "no embed here"); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected the retry call and the follow-up after the artifact notification, got %d LLM calls", len(calls))
	}
	if len(calls[0].StopSequences) != 0 {
		t.Errorf("the retry call must not carry stop sequences, got %q", calls[0].StopSequences)
	}
	if !slices.Equal(calls[1].StopSequences, fabricatedTurnStops) {
		t.Errorf("the follow-up after the notify StopSequences = %q, want %q", calls[1].StopSequences, fabricatedTurnStops)
	}
}
