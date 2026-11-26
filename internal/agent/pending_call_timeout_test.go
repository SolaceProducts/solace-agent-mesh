// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func withShortAuthTimeout(t *testing.T, agent *Agent, d time.Duration) {
	t.Helper()
	agent.SetAuthPendingTimeout(d)
}

func TestPendingAuthTimeout_FiresAndResumes(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-t1"))
	withShortAuthTimeout(t, agent, 50*time.Millisecond)
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)
	_, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("stubtool", "call-t1", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)
	if !paused {
		t.Fatal("expected pause")
	}

	deadline := time.After(2 * time.Second)
	for resumeCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("pending-auth timeout did not invoke resume within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.PendingCalls["call-t1"] != nil {
		t.Error("pending call still registered after timeout")
	}
	if r := state.SyncResults[0]; r == nil || r.Status != tool.StatusError {
		t.Errorf("SyncResults[0] = %+v, want timeout error", r)
	}
}

func TestPendingAuthTimeout_CancelStopsTimer(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-c1"))
	withShortAuthTimeout(t, agent, 50*time.Millisecond)
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)
	_, _ = agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("stubtool", "call-c1", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	time.Sleep(150 * time.Millisecond)
	if resumeCalls.Load() != 0 {
		t.Errorf("resume hook called %d times after cancel; timer was not stopped", resumeCalls.Load())
	}
}

func TestCancelPausedTask_StopsAuthTimer(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-c"))
	withShortAuthTimeout(t, agent, 50*time.Millisecond)
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)
	_, _ = agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("stubtool", "call-c", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	time.Sleep(200 * time.Millisecond)

	if agent.pausedTasks.Load(state.TaskID) != nil {
		t.Error("paused task should be removed after cancel")
	}
	if resumeCalls.Load() != 0 {
		t.Errorf("resume hook called %d times after cancel; timer was not stopped", resumeCalls.Load())
	}
}

func TestDispatcher_PendingAuth_SetsTimeout(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-am"))
	agent.SetAuthPendingTimeout(75 * time.Millisecond)
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	_, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("stubtool", "call-am", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)
	if !paused {
		t.Fatal("expected pause")
	}

	pc := state.PendingCalls["call-am"]
	if pc == nil {
		t.Fatal("no pending call registered")
	}
	if pc.Kind != PendingCallAuth {
		t.Errorf("Kind = %v, want PendingCallAuth", pc.Kind)
	}
	if pc.Timeout != 75*time.Millisecond {
		t.Errorf("Timeout = %v, want 75ms (per-Agent override)", pc.Timeout)
	}
	if pc.TimeoutTimer == nil {
		t.Error("TimeoutTimer should be set")
	}
	pc.TimeoutTimer.Stop()
}

func TestPendingAuthTimeout_PerAgentIsolation(t *testing.T) {
	agentA := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-a"))
	agentA.SetAuthPendingTimeout(50 * time.Millisecond)
	stateA := newTestTaskState(agentA)
	pcaA := defaultPauseCallArgs(agentA, stateA)

	agentB := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-b"))
	agentB.SetAuthPendingTimeout(200 * time.Millisecond)
	stateB := newTestTaskState(agentB)
	pcaB := defaultPauseCallArgs(agentB, stateB)

	if _, paused := agentA.dispatchToolCallsMayPause(
		t.Context(), stateA,
		singleCall("stubtool", "call-a", "{}"),
		pcaA.toolCtx, pcaA.sp, pcaA.parallelGroupID, pcaA.sanitizeErrors, agentA.logger,
	); !paused {
		t.Fatal("agentA: expected pause")
	}
	if _, paused := agentB.dispatchToolCallsMayPause(
		t.Context(), stateB,
		singleCall("stubtool", "call-b", "{}"),
		pcaB.toolCtx, pcaB.sp, pcaB.parallelGroupID, pcaB.sanitizeErrors, agentB.logger,
	); !paused {
		t.Fatal("agentB: expected pause")
	}

	pcA := stateA.PendingCalls["call-a"]
	pcB := stateB.PendingCalls["call-b"]
	if pcA == nil || pcB == nil {
		t.Fatalf("missing pending call: A=%v B=%v", pcA, pcB)
	}
	if pcA.Timeout != 50*time.Millisecond {
		t.Errorf("agentA Timeout = %v, want 50ms", pcA.Timeout)
	}
	if pcB.Timeout != 200*time.Millisecond {
		t.Errorf("agentB Timeout = %v, want 200ms", pcB.Timeout)
	}
	pcA.TimeoutTimer.Stop()
	pcB.TimeoutTimer.Stop()
}

func withShortHILTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := hilApprovalDefaultTimeout
	hilApprovalDefaultTimeout = d
	t.Cleanup(func() { hilApprovalDefaultTimeout = prev })
}

func TestPendingHILTimeout_FiresAndResumes(t *testing.T) {
	withShortHILTimeout(t, 50*time.Millisecond)

	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "test_hil_tool", stubReturnsPendingHIL("call-h1", "hil-req-1"))
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)
	_, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("test_hil_tool", "call-h1", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)
	if !paused {
		t.Fatal("expected pause")
	}

	deadline := time.After(2 * time.Second)
	for resumeCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("pending-HIL timeout did not invoke resume within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.PendingCalls["call-h1"] != nil {
		t.Error("pending call still registered after timeout")
	}
	r := state.SyncResults[0]
	if r == nil {
		t.Fatal("SyncResults[0] is nil after HIL timeout")
	}
	if r.Status != tool.StatusSuccess {
		t.Errorf("SyncResults[0].Status = %v, want StatusSuccess (timed_out approval result)", r.Status)
	}
	if r.Data["status"] != "timed_out" {
		t.Errorf("SyncResults[0].Data[status] = %v, want timed_out", r.Data["status"])
	}
}

func TestPendingHILTimeout_CancelStopsTimer(t *testing.T) {
	withShortHILTimeout(t, 50*time.Millisecond)

	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "test_hil_tool", stubReturnsPendingHIL("call-hc", "hil-req-cancel"))
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)
	_, _ = agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("test_hil_tool", "call-hc", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)

	agent.cancelPausedTask(state, cancelFromElsewhere)

	time.Sleep(150 * time.Millisecond)
	if resumeCalls.Load() != 0 {
		t.Errorf("resume hook called %d times after cancel; timer was not stopped", resumeCalls.Load())
	}
}

func TestDispatcher_PendingHIL_SetsHILRequestIDAndTimeout(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "test_hil_tool", stubReturnsPendingHIL("call-hm", "hil-meta-1"))
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	_, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("test_hil_tool", "call-hm", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)
	if !paused {
		t.Fatal("expected pause")
	}

	pc := state.PendingCalls["call-hm"]
	if pc == nil {
		t.Fatal("no pending call registered")
	}
	if pc.Kind != PendingCallHILApproval {
		t.Errorf("Kind = %v, want PendingCallHILApproval", pc.Kind)
	}
	if pc.HILRequestID != "hil-meta-1" {
		t.Errorf("HILRequestID = %q, want %q", pc.HILRequestID, "hil-meta-1")
	}
	if pc.Timeout != hilApprovalDefaultTimeout {
		t.Errorf("Timeout = %v, want default %v", pc.Timeout, hilApprovalDefaultTimeout)
	}
	if pc.TimeoutTimer == nil {
		t.Error("TimeoutTimer should be set")
	}
	pc.TimeoutTimer.Stop()
}

func TestDispatcher_PendingHIL_CustomTimeoutFromData(t *testing.T) {
	customTimeout := 5 * time.Minute
	agent := newTestAgentWithStubTool(t, "test_hil_tool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return &tool.Result{
			Status:  tool.StatusPending,
			Message: "Waiting...",
			Data: map[string]any{
				"pending_kind":   "hil_approval",
				"hil_request_id": "hil-custom",
				"hil_timeout_ns": float64(customTimeout.Nanoseconds()),
			},
		}, nil
	})
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	_, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("test_hil_tool", "call-ct", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)
	if !paused {
		t.Fatal("expected pause")
	}

	pc := state.PendingCalls["call-ct"]
	if pc == nil {
		t.Fatal("no pending call registered")
	}
	if pc.Timeout != customTimeout {
		t.Errorf("Timeout = %v, want custom %v", pc.Timeout, customTimeout)
	}
	pc.TimeoutTimer.Stop()
}
