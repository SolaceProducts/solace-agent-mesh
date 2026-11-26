// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestAgentTurn_AsksForTheModelsStoredParams(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "done", FinishReason: "stop"})

	runTask(t, agent)

	if len(mock.calls) == 0 {
		t.Fatal("the agent made no LLM call")
	}
	if !mock.calls[0].WithStoredParams {
		t.Error("the agent turn did not ask for the parameters stored on its model, so a user's saved settings never reach a chat")
	}
}

func TestCompaction_SummaryCallLeavesOutTheModelsStoredParams(t *testing.T) {
	mock := newMockLLM(&llm.ChatResponse{Content: "condensed history"})
	a := New(AgentConfig{Name: "test-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 5})
	svc := newMockServices(mock, newMemSessionStore())
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	sess := &session.Session{Messages: []session.Message{
		{Role: "user", Content: "First question with enough words to be worth summarizing later on"},
		{Role: "assistant", Content: "First answer with enough words to be worth summarizing later on"},
		{Role: "user", Content: "Second question with enough words to be worth summarizing later on"},
		{Role: "assistant", Content: "Second answer with enough words to be worth summarizing later on"},
		{Role: "user", Content: "Third question"},
		{Role: "assistant", Content: "Third answer"},
	}}

	if _, err := a.performCompaction(t.Context(), sess, CompactionConfig{Enabled: true, CompactionPercentage: 0.5}, nil); err != nil {
		t.Fatalf("performCompaction: %v", err)
	}

	if len(mock.calls) == 0 {
		t.Fatal("compaction made no LLM call")
	}
	if mock.calls[0].WithStoredParams {
		t.Error("the compaction summary asked for the chat settings stored on the model")
	}
}
