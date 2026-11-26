// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"
)

func TestFindingThePersonWaitOfATaskMatchesOnlyItsOwnDurableWaitForThatRequestId(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.TaskID = "task-with-three-waits"
	for i, w := range []struct {
		callID, requestID string
		notYetDurable     bool
	}{
		{callID: "call-a", requestID: "req-a"},
		{callID: "call-b", requestID: "req-b"},
		{callID: "call-c", requestID: "req-c", notYetDurable: true},
	} {
		state.PendingCalls[w.callID] = &PendingToolCall{
			ToolCallID: w.callID, ToolName: "ask_person", Kind: PendingCallHILApproval, Index: i,
			HILRequestID: w.requestID, Timeout: time.Hour, notYetDurable: w.notYetDurable,
		}
	}
	agent.pausedTasks.Store(state)
	tests := []struct {
		name      string
		taskID    string
		requestID string
		want      string
	}{
		{name: "the first wait by its id", taskID: state.TaskID, requestID: "req-a", want: "call-a"},
		{name: "the second wait by its id", taskID: state.TaskID, requestID: "req-b", want: "call-b"},
		{name: "a wait whose checkpoint is not written yet", taskID: state.TaskID, requestID: "req-c"},
		{name: "an id no wait of the task uses", taskID: state.TaskID, requestID: "req-none"},
		{name: "an id the task waits on, asked for another task", taskID: "another-task", requestID: "req-a"},
	}
	const lookups = 25
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range lookups {
				_, pc := agent.pausedTasks.FindHILWaitOfTask(tt.taskID, tt.requestID)

				got := ""
				if pc != nil {
					got = pc.ToolCallID
				}
				if got != tt.want {
					t.Fatalf("wait found = %q, want %q", got, tt.want)
				}
			}
		})
	}
}
