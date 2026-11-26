// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

type cannedCheckpoint struct {
	session.Store
	cp *session.TaskCheckpoint
}

func (s cannedCheckpoint) GetCheckpoint(context.Context, string) (*session.TaskCheckpoint, error) {
	return s.cp, nil
}

func TestLoginTimeoutReportsAFailureAnotherReplicaRecordedWithoutCountingItAgainstTheUser(t *testing.T) {
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
	agent, _ := newLoginAgent(t, nil)
	state := loginPausedState(t, agent)
	agent.pausedTasks.Store(state)
	recorded := &session.ToolCallResult{ToolCallID: loginCallID, ToolName: "stubtool", Content: "the sign-in was refused on another replica", IsError: true}
	if _, err := agent.checkpoints.store.RecordToolResult(context.Background(), loginTaskID, authCorrelationID(loginTaskID, loginCallID), recorded); err != nil {
		t.Fatalf("record on the other replica: %v", err)
	}

	agent.handlePendingAuthTimeout(loginTaskID, loginCallID)

	state.mu.Lock()
	outcome := state.pendingAuthOutcome
	state.mu.Unlock()
	if outcome == nil || outcome.success {
		t.Errorf("auth outcome = %+v, want a failure: the login failed on the other replica", outcome)
	}
	agent.authInFlightMu.Lock()
	defer agent.authInFlightMu.Unlock()
	if failures := agent.authFailures[authFlightKey(state.UserProps.UserID, state.UserProps.TraceID, loginKey)]; failures != nil {
		t.Errorf("auth failures = %+v, want none counted: this replica's timeout did not decide the call", failures)
	}
}

func TestRecordedResultFillsTheCallsOwnSlot(t *testing.T) {
	recordedByCall := &session.TaskCheckpoint{ToolResults: []session.ToolCallResult{
		{ToolCallID: "call-a", Content: "first"},
		{ToolCallID: "call-b", Content: "second"},
	}}
	tests := []struct {
		name       string
		toolCallID string
		index      int
		want       map[int]string
	}{
		{name: "the first call", toolCallID: "call-a", index: 0, want: map[int]string{0: "first"}},
		{name: "the second call", toolCallID: "call-b", index: 1, want: map[int]string{1: "second"}},
		{name: "a call nothing was recorded for", toolCallID: "call-c", index: 2, want: map[int]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _ := newLoginAgent(t, nil)
			agent.checkpoints.store = cannedCheckpoint{Store: agent.checkpoints.store, cp: recordedByCall}
			state := newTestTaskState(agent)

			agent.useRecordedResult(t.Context(), state, &PendingToolCall{ToolCallID: tt.toolCallID, Index: tt.index})

			got := make(map[int]string)
			state.mu.Lock()
			for index, result := range state.SyncResults {
				got[index] = result.Message
			}
			state.mu.Unlock()
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("SyncResults messages by index (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRelayRefusesAStrangersAnswerAndLogsTheRefusal(t *testing.T) {
	base := session.NewMemoryStore()
	holder, _, _ := replicaOf(t, base, "holder")
	logs := &syncLogBuffer{}
	other, _, _ := replicaOf(t, base, "other", capturingLogs(logs))
	pausedOnQuestion(t, base, holder, "task-ask")

	delivery := other.relayUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(strangerID), nil)

	if delivery != hilRefused {
		t.Errorf("relayUserInputResponse() = %v, want refused", delivery)
	}
	refusals := logs.records(refusedAnswerLog)
	if len(refusals) != 1 {
		t.Fatalf("refusal records = %v, want exactly one", refusals)
	}
	want := map[string]any{"level": "WARN", "userID": strangerID, "taskID": "task-ask", "requestID": askRequestID}
	got := map[string]any{"level": refusals[0]["level"], "userID": refusals[0]["userID"], "taskID": refusals[0]["taskID"], "requestID": refusals[0]["requestID"]}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refusal record mismatch (-want +got):\n%s", diff)
	}
}
