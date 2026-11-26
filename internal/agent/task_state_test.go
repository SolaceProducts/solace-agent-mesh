// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync"
	"testing"
)

func TestPendingToolCall_AuthFields(t *testing.T) {
	pc := &PendingToolCall{
		ToolCallID:   "call-1",
		ToolName:     "atlassian__userinfo",
		Kind:         PendingCallAuth,
		OriginalArgs: map[string]any{"cloud_id": "abc"},
		AuthRetries:  1,
	}
	if pc.Kind != PendingCallAuth {
		t.Errorf("Kind = %v, want PendingCallAuth", pc.Kind)
	}
	if got := pc.OriginalArgs["cloud_id"]; got != "abc" {
		t.Errorf("OriginalArgs[cloud_id] = %v, want abc", got)
	}
	if pc.AuthRetries != 1 {
		t.Errorf("AuthRetries = %d, want 1", pc.AuthRetries)
	}
	if pc.ToolCallID != "call-1" || pc.ToolName != "atlassian__userinfo" {
		t.Errorf("identity fields not preserved: ToolCallID=%q ToolName=%q", pc.ToolCallID, pc.ToolName)
	}
}

func TestPendingCallKind_AuthValue(t *testing.T) {
	if PendingCallPeer != 0 {
		t.Errorf("PendingCallPeer = %d, want 0", PendingCallPeer)
	}
	if PendingCallAuth <= PendingCallHILAsk {
		t.Errorf("PendingCallAuth (%d) must be appended after existing kinds, not inserted", PendingCallAuth)
	}
}

func TestTaskStateStore_StoreAndLoad(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	state := &TaskExecutionState{TaskID: "task-1"}
	store.Store(state)

	got := store.Load("task-1")
	if got != state {
		t.Errorf("Load returned %v, want stored state", got)
	}

	if store.Load("nonexistent") != nil {
		t.Error("Load of unknown ID should return nil")
	}
}

func TestTaskStateStore_Delete(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	store.Store(&TaskExecutionState{TaskID: "task-1"})
	store.Delete("task-1")

	if store.Load("task-1") != nil {
		t.Error("Load after Delete should return nil")
	}
}

func TestTaskStateStore_Overwrite(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	store.Store(&TaskExecutionState{TaskID: "task-1", MaxCalls: 5})
	store.Store(&TaskExecutionState{TaskID: "task-1", MaxCalls: 10})

	got := store.Load("task-1")
	if got.MaxCalls != 10 {
		t.Errorf("MaxCalls = %d, want 10 (overwrite)", got.MaxCalls)
	}
}

func TestTaskStateStore_FindByHILRequestID_Found(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	state := &TaskExecutionState{
		TaskID: "task-1",
		PendingCalls: map[string]*PendingToolCall{
			"call-1": {
				ToolCallID:   "call-1",
				Kind:         PendingCallHILApproval,
				HILRequestID: "req-abc",
			},
		},
	}
	store.Store(state)

	gotState, gotPC := store.FindByHILRequestID("req-abc")
	if gotState == nil || gotPC == nil {
		t.Fatal("expected to find state and pending call")
	}
	if gotState.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", gotState.TaskID, "task-1")
	}
	if gotPC.HILRequestID != "req-abc" {
		t.Errorf("HILRequestID = %q, want %q", gotPC.HILRequestID, "req-abc")
	}
}

func TestTaskStateStore_FindByHILRequestID_NotFound(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	store.Store(&TaskExecutionState{
		TaskID:       "task-1",
		PendingCalls: map[string]*PendingToolCall{},
	})

	gotState, gotPC := store.FindByHILRequestID("nonexistent")
	if gotState != nil || gotPC != nil {
		t.Error("expected nil for nonexistent HIL request ID")
	}
}

func TestTaskStateStore_FindByHILRequestID_IgnoresOtherKinds(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	state := &TaskExecutionState{
		TaskID: "task-1",
		PendingCalls: map[string]*PendingToolCall{
			"call-1": {
				ToolCallID:   "call-1",
				Kind:         PendingCallPeer,
				HILRequestID: "req-abc",
			},
		},
	}
	store.Store(state)

	gotState, gotPC := store.FindByHILRequestID("req-abc")
	if gotState != nil || gotPC != nil {
		t.Error("FindByHILRequestID should ignore calls that do not wait on the user")
	}
}

func TestTaskStateStore_FindByHILRequestID_FindsAQuestion(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()
	store.Store(&TaskExecutionState{
		TaskID: "task-1",
		PendingCalls: map[string]*PendingToolCall{
			"call-1": {ToolCallID: "call-1", Kind: PendingCallHILAsk, HILRequestID: "req-abc"},
		},
	})

	if _, gotPC := store.FindByHILRequestID("req-abc"); gotPC == nil || gotPC.ToolCallID != "call-1" {
		t.Errorf("FindByHILRequestID = %+v; a task paused on a question must be found by its request id, or its answer is dropped", gotPC)
	}
}

func TestTaskStateStore_FindByHILRequestID_MultipleStates(t *testing.T) {
	t.Parallel()
	store := newTaskStateStore()

	store.Store(&TaskExecutionState{
		TaskID: "task-1",
		PendingCalls: map[string]*PendingToolCall{
			"call-1": {
				ToolCallID:   "call-1",
				Kind:         PendingCallHILApproval,
				HILRequestID: "req-other",
			},
		},
	})

	store.Store(&TaskExecutionState{
		TaskID: "task-2",
		PendingCalls: map[string]*PendingToolCall{
			"call-2": {
				ToolCallID:   "call-2",
				Kind:         PendingCallHILApproval,
				HILRequestID: "req-target",
			},
		},
	})

	gotState, gotPC := store.FindByHILRequestID("req-target")
	if gotState == nil || gotPC == nil {
		t.Fatal("expected to find state and pending call")
	}
	if gotState.TaskID != "task-2" {
		t.Errorf("TaskID = %q, want %q", gotState.TaskID, "task-2")
	}
	if gotPC.ToolCallID != "call-2" {
		t.Errorf("ToolCallID = %q, want %q", gotPC.ToolCallID, "call-2")
	}
}

func TestDecrementPending(t *testing.T) {
	t.Parallel()
	state := &TaskExecutionState{TotalPending: 3}

	if got := state.DecrementPending(); got != 2 {
		t.Errorf("first decrement = %d, want 2", got)
	}
	if got := state.DecrementPending(); got != 1 {
		t.Errorf("second decrement = %d, want 1", got)
	}
	if got := state.DecrementPending(); got != 0 {
		t.Errorf("third decrement = %d, want 0", got)
	}
}

func TestDecrementPending_Concurrent(t *testing.T) {
	t.Parallel()
	const n = 100
	state := &TaskExecutionState{TotalPending: n}

	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			state.DecrementPending()
		})
	}
	wg.Wait()

	if state.TotalPending != 0 {
		t.Errorf("TotalPending = %d after %d concurrent decrements, want 0", state.TotalPending, n)
	}
}
