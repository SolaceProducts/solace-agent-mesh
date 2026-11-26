// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"maps"
	"slices"
	"testing"
	"time"
)

func TestFindingAPersonWaitByRequestIdIgnoresAWaitWhoseCheckpointIsNotWrittenYet(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.TaskID = "task-with-two-waits"
	for i, w := range []struct {
		callID, requestID string
		notYetDurable     bool
	}{
		{callID: "call-durable", requestID: "req-durable"},
		{callID: "call-pending-write", requestID: "req-pending-write", notYetDurable: true},
	} {
		state.PendingCalls[w.callID] = &PendingToolCall{
			ToolCallID: w.callID, ToolName: "ask_person", Kind: PendingCallHILApproval, Index: i,
			HILRequestID: w.requestID, Timeout: time.Hour, notYetDurable: w.notYetDurable,
		}
	}
	agent.pausedTasks.Store(state)
	tests := []struct {
		name      string
		requestID string
		wantCall  string
		wantHeld  bool
	}{
		{name: "a wait whose checkpoint is written is found and held", requestID: "req-durable", wantCall: "call-durable", wantHeld: true},
		{name: "a wait whose checkpoint is not written yet is neither found nor held", requestID: "req-pending-write"},
		{name: "an id no wait uses is neither found nor held", requestID: "req-none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, pc := agent.pausedTasks.FindByHILRequestID(tt.requestID)

			gotCall := ""
			if pc != nil {
				gotCall = pc.ToolCallID
			}
			if gotCall != tt.wantCall {
				t.Errorf("FindByHILRequestID(%q) found call %q, want %q", tt.requestID, gotCall, tt.wantCall)
			}
			if got := agent.pausedTasks.holdsHILRequestID(tt.requestID); got != tt.wantHeld {
				t.Errorf("holdsHILRequestID(%q) = %v, want %v", tt.requestID, got, tt.wantHeld)
			}
		})
	}
}

func TestTheSweepForAnswersRecordedElsewhereChecksOnlyPersonWaitsWhoseCheckpointIsWritten(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.TaskID = "task-with-mixed-waits"
	for i, c := range []struct {
		callID        string
		kind          PendingCallKind
		notYetDurable bool
	}{
		{callID: "call-durable-person", kind: PendingCallHILApproval},
		{callID: "call-pending-write-person", kind: PendingCallHILAsk, notYetDurable: true},
		{callID: "call-remote-tool", kind: PendingCallRemoteTool},
	} {
		state.PendingCalls[c.callID] = &PendingToolCall{
			ToolCallID: c.callID, ToolName: "wait_for_it", Kind: c.kind, Index: i,
			HILRequestID: "req-" + c.callID, Timeout: time.Hour, notYetDurable: c.notYetDurable,
		}
	}

	got := slices.Sorted(maps.Keys(callsAnsweredElsewhere(state)))

	if want := []string{"call-durable-person"}; !slices.Equal(got, want) {
		t.Errorf("callsAnsweredElsewhere() = %v, want %v", got, want)
	}
}
