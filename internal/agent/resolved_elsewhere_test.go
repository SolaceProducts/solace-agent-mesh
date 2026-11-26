// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func toolMessagesMention(calls []llm.ChatRequest, text string) bool {
	for _, req := range calls {
		for _, msg := range req.Messages {
			if msg.Role == "tool" && strings.Contains(msg.Content, text) {
				return true
			}
		}
	}
	return false
}

func TestHolderTimeoutNeverOverridesAnAnswerAnotherReplicaRecorded(t *testing.T) {
	store := session.NewMemoryStore()
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder")
	other, _, _ := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}

	holder.handlePendingHILTimeout("task-ask", "tc-1")

	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
	if toolMessagesMention(holderLLM.getCalls(), "timed_out") {
		t.Error("the resumed turn was told the user did not answer although another replica recorded the answer")
	}
}

func TestHolderKeepsTheRecordedAnswerWhenADifferentOneReachesItLater(t *testing.T) {
	store := session.NewMemoryStore()
	holder, holderLLM, holderBrk := replicaOf(t, store, "holder")
	other, _, _ := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	if got := other.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}
	second := regionAnswer()
	second.Data = map[string]any{"answers": map[string]any{"q0": "APAC"}}

	if got := holder.deliverHILResponse(second, hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}

	assertResumedToCompletion(t, holder, holderLLM, holderBrk, "task-ask", "EMEA")
	if toolMessagesMention(holderLLM.getCalls(), "APAC") {
		t.Error("the resumed turn ran with the second answer although the store holds the first")
	}
}

func TestLoginTimeoutNeverOverridesAResultAnotherReplicaRecorded(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	agent, _ := newLoginAgent(t, nil)
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	recorded := &session.ToolCallResult{ToolCallID: loginCallID, ToolName: "stubtool", Content: "signed in on another replica"}
	if _, err := agent.checkpoints.store.RecordToolResult(context.Background(), loginTaskID, authCorrelationID(loginTaskID, loginCallID), recorded); err != nil {
		t.Fatalf("record on the other replica: %v", err)
	}

	agent.handlePendingAuthTimeout(loginTaskID, loginCallID)

	state.mu.Lock()
	defer state.mu.Unlock()
	if got := state.SyncResults[0]; got == nil || got.Message != "signed in on another replica" {
		t.Errorf("SyncResults[0] = %+v, want the result the other replica recorded", got)
	}
	if outcome := state.pendingAuthOutcome; outcome == nil || !outcome.success {
		t.Errorf("auth outcome = %+v, want a success: the login completed on the other replica", outcome)
	}
}
