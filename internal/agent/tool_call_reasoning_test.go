// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const (
	lookupReasoning        = "The user wants the report, so I call the lookup tool."
	saveReportReasoning    = "I will save the report as an artifact."
	notifyArtifactSaveTool = "_notify_artifact_save"
)

var reasoningThenLookupTurn = []llm.StreamChunk{
	{ReasoningDelta: "The user wants the report, "},
	{ReasoningDelta: "so I call the lookup tool."},
	{ToolCallDeltas: []llm.ToolCallDelta{{Index: 0, ID: "call-lookup", Name: "lookup", ArgumentsDelta: "{}"}}},
	{FinishReason: "tool_calls"},
}

func finalAnswerTurn(text string) []llm.StreamChunk {
	return []llm.StreamChunk{{ContentDelta: text}, {FinishReason: "stop"}}
}

func reasoningThenArtifactBlock(block string) []llm.StreamChunk {
	return []llm.StreamChunk{{ReasoningDelta: saveReportReasoning}, {ContentDelta: block}, {FinishReason: "stop"}}
}

func toolCallReasoning(t *testing.T, msgs []llm.ChatMessage, toolName string) string {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role == "assistant" && len(m.ToolCalls) == 1 && m.ToolCalls[0].Name == toolName {
			return m.Reasoning
		}
	}
	t.Fatalf("no assistant turn calling %s in the request: %+v", toolName, msgs)
	return ""
}

func newReasoningAgent(t *testing.T, llmMock llm.Client) *Agent {
	t.Helper()
	agent := New(AgentConfig{
		Name:        "reasoning-agent",
		Model:       "test-model",
		MaxTokens:   1000,
		MaxLLMCalls: 5,
		Namespace:   "test-ns",
	})
	svc := newMockServices(llmMock, newMemSessionStore())
	agent.svc = svc
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agent
}

func TestToolCallReasoningIsReplayedOnLaterLLMCalls(t *testing.T) {
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{
		reasoningThenLookupTurn,
		finalAnswerTurn("Report built."),
		finalAnswerTurn("Here it is again."),
	}}
	agent := newReasoningAgent(t, llmMock)
	registerCountingTool(t, agent, "lookup")

	runTask(t, agent)
	if _, err := agent.HandleTask(t.Context(), "task-002", newUserMessage("Show it again"), testUser); err != nil {
		t.Fatalf("HandleTask(task-002, same context): %v", err)
	}

	if got := toolCallReasoning(t, llmMock.request(t, 1), "lookup"); got != lookupReasoning {
		t.Errorf("tool loop request: reasoning on the lookup turn = %q, want %q", got, lookupReasoning)
	}
	if got := toolCallReasoning(t, llmMock.request(t, 2), "lookup"); got != lookupReasoning {
		t.Errorf("next user turn: reasoning on the stored lookup turn = %q, want %q", got, lookupReasoning)
	}
}

func TestBlockValidationRetryKeepsTheRejectedTurnsReasoning(t *testing.T) {
	cases := []struct {
		name  string
		block string
	}{
		{name: "header rejected when the block starts", block: "«««save_artifact: mime_type=\"text/plain\"\nreport\n»»»\n"},
		{name: "unclosed block rejected when the block ends", block: "«««save_artifact: filename=\"report.txt\" mime_type=\"text/plain\"\nreport\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{
				reasoningThenArtifactBlock(tc.block),
				finalAnswerTurn("Saved."),
			}}
			agent := newReasoningAgent(t, llmMock)

			runTask(t, agent)

			if got := toolCallReasoning(t, llmMock.request(t, 1), notifyArtifactSaveTool); got != saveReportReasoning {
				t.Errorf("retry request: reasoning on the rejected artifact turn = %q, want %q", got, saveReportReasoning)
			}
		})
	}
}

func TestStructuredInvocationArtifactFollowUpKeepsTheTurnsReasoning(t *testing.T) {
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{
		outputWithoutResultEmbed,
		reasoningThenArtifactBlock("«««save_artifact: filename=\"summary.json\" mime_type=\"application/json\"\n{\"summary\": \"ok\"}\n»»»\n"),
		outputWithResultEmbed,
	}}

	runStructuredInvocationWithMetadata(t.Context(), t, "task-si-followup-reasoning", llmMock, 1, nil)

	if got := toolCallReasoning(t, llmMock.request(t, 2), notifyArtifactSaveTool); got != saveReportReasoning {
		t.Errorf("artifact follow-up request: reasoning on the artifact turn = %q, want %q", got, saveReportReasoning)
	}
}
