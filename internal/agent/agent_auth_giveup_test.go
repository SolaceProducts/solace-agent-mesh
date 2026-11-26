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

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestAuthGiveUp_ClearedOnSuccessViaHandleAuthResponse(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})
	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("ok"), nil
	})
	agent.SetAuthMaxAttempts(1)

	const taskUser, taskTrace = "User-Task@example.com", "trace-x"

	state := newTestTaskState(agent)
	state.TaskID = "task-clear"
	state.UserProps.UserID = taskUser
	state.UserProps.TraceID = taskTrace
	state.TotalPending = 1
	state.PendingCalls["call-1"] = &PendingToolCall{
		ToolCallID:    "call-1",
		ToolName:      "stubtool",
		Kind:          PendingCallAuth,
		Index:         0,
		OriginalArgs:  map[string]any{"q": "test"},
		CredentialKey: "test-key",
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-1", "stubtool", `{"q":"test"}`)
	agent.pausedTasks.Store(state)
	registerStubAuthHandler(t, agent, "test-key")

	agent.recordAuthFailure(taskUser, taskTrace, "test-key")
	if !agent.authGaveUp(taskUser, taskTrace, "test-key") {
		t.Fatal("precondition: credential must be given up before the successful auth")
	}

	resp := authResp("task-clear", "call-1", "test-key")
	if _, err := agent.handleAuthResponse(t.Context(), "task-clear", resp, a2a.UserProperties{UserID: "user-task@EXAMPLE.com"}); err != nil {
		t.Fatalf("handleAuthResponse err = %v", err)
	}

	if agent.authGaveUp(taskUser, taskTrace, "test-key") {
		t.Error("a successful auth must clear the give-up tally for the STATE's (userID, traceID) — not the envelope's")
	}
}

func stubPendingAuthWithCred(credKey string) stubExecFn {
	return func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return toolauth.NewPendingAuthResult("fc", args, credKey), nil
	}
}

func stubAuthAwareTool(credKey string) stubExecFn {
	return func(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		if toolCtx.AuthGaveUp != nil && toolCtx.AuthGaveUp(toolCtx.UserID, toolCtx.TraceID, credKey) {
			return toolauth.NewAuthGaveUpResult("stubtool", ""), nil
		}
		return toolauth.NewPendingAuthResult("fc", args, credKey), nil
	}
}

func stubPendingAuthFollowerWithCred(credKey string) stubExecFn {
	return func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return toolauth.NewPendingAuthFollowerResult("fc", args, credKey), nil
	}
}

func TestAuthGiveUp_ToolCtxWiresAuthGaveUp(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubPendingAuthWithCred("cred-x"))
	agent.SetAuthMaxAttempts(1)
	state := newTestTaskState(agent)
	state.UserProps.TraceID = "trace-x"
	pca := defaultPauseCallArgs(agent, state)

	toolCtx := agent.buildToolCtxFromState(t.Context(), state, pca.sp)
	if toolCtx.AuthGaveUp == nil {
		t.Fatal("buildToolCtxFromState must wire AuthGaveUp — a nil callback disables the give-up guard")
	}
	if toolCtx.AuthGaveUp("u-test", "trace-x", "cred-x") {
		t.Error("no failures recorded: AuthGaveUp must be false")
	}
	agent.recordAuthFailure("u-test", "trace-x", "cred-x")
	if !toolCtx.AuthGaveUp("u-test", "trace-x", "cred-x") {
		t.Error("after reaching the cap, the wired AuthGaveUp must report give-up")
	}
}

func TestAuthGiveUp_DispatchFastFailsAfterCap(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubAuthAwareTool("cred-x"))
	agent.SetAuthMaxAttempts(2)

	fresh := newTestTaskState(agent)
	fresh.UserProps.TraceID = "trace-x"
	freshArgs := defaultPauseCallArgs(agent, fresh)
	freshCtx := agent.buildToolCtxFromState(t.Context(), fresh, freshArgs.sp)
	if _, paused := agent.dispatchToolCallsMayPause(
		t.Context(), fresh, singleCall("stubtool", "call-a", "{}"),
		freshCtx, freshArgs.sp, freshArgs.parallelGroupID, freshArgs.sanitizeErrors, agent.logger,
	); !paused {
		t.Fatal("below the cap the auth-aware tool must pause (prompt the user)")
	}
	if pc := fresh.PendingCalls["call-a"]; pc != nil && pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}

	agent.recordAuthFailure("u-test", "trace-x", "cred-x")
	agent.recordAuthFailure("u-test", "trace-x", "cred-x")

	givenUp := newTestTaskState(agent)
	givenUp.TaskID = "task-2"
	givenUp.UserProps.TraceID = "trace-x"
	guArgs := defaultPauseCallArgs(agent, givenUp)
	guCtx := agent.buildToolCtxFromState(t.Context(), givenUp, guArgs.sp)
	results, paused := agent.dispatchToolCallsMayPause(
		t.Context(), givenUp, singleCall("stubtool", "call-b", "{}"),
		guCtx, guArgs.sp, guArgs.parallelGroupID, guArgs.sanitizeErrors, agent.logger,
	)
	if paused {
		t.Fatal("after the give-up cap, the tool must fail fast — the task must NOT pause")
	}
	if givenUp.PendingCalls["call-b"] != nil {
		t.Error("no PendingCallAuth may be registered once auth is given up")
	}
	if len(results) != 1 || results[0] == nil || results[0].Status != tool.StatusError {
		t.Errorf("expected a single terminal error result, got %+v", results)
	}
}

func TestAuthGiveUp_FollowerTimeoutDoesNotCount(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubPendingAuthFollowerWithCred("cred-x"))
	agent.SetAuthPendingTimeout(40 * time.Millisecond)
	agent.SetAuthMaxAttempts(1)

	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	state := newTestTaskState(agent)
	state.UserProps.TraceID = "trace-x"
	pca := defaultPauseCallArgs(agent, state)
	if _, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state, singleCall("stubtool", "call-f", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	); !paused {
		t.Fatal("expected pause on follower pending-auth")
	}

	deadline := time.After(2 * time.Second)
	for resumeCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("follower timeout did not fire within 2s")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if agent.authGaveUp("u-test", "trace-x", "cred-x") {
		t.Error("a FOLLOWER timeout must not count toward the give-up cap")
	}
}

func TestAuthGiveUp_TripsAtCapAndScopes(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.SetAuthMaxAttempts(2)

	if agent.authGaveUp("u1", "trace-1", "cred") {
		t.Fatal("no failures recorded yet: must not give up")
	}
	if got := agent.recordAuthFailure("u1", "trace-1", "cred"); got != 1 {
		t.Fatalf("first failure count = %d, want 1", got)
	}
	if agent.authGaveUp("u1", "trace-1", "cred") {
		t.Error("one timeout (< cap 2): must NOT give up — the user still gets a second prompt")
	}
	if got := agent.recordAuthFailure("u1", "trace-1", "cred"); got != 2 {
		t.Fatalf("second failure count = %d, want 2", got)
	}
	if !agent.authGaveUp("u1", "trace-1", "cred") {
		t.Error("two timeouts (== cap 2): must give up")
	}

	if agent.authGaveUp("u2", "trace-1", "cred") {
		t.Error("different user must not inherit give-up")
	}
	if agent.authGaveUp("u1", "trace-2", "cred") {
		t.Error("different trace must not inherit give-up")
	}
	if agent.authGaveUp("u1", "trace-1", "other") {
		t.Error("different credential must not inherit give-up")
	}
}

func TestAuthGiveUp_ClearedOnSuccess(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.SetAuthMaxAttempts(1)

	agent.recordAuthFailure("u1", "trace-1", "cred")
	if !agent.authGaveUp("u1", "trace-1", "cred") {
		t.Fatal("precondition: given up at cap 1")
	}
	agent.clearAuthFailures("u1", "trace-1", "cred")
	if agent.authGaveUp("u1", "trace-1", "cred") {
		t.Error("after a successful auth the tally must reset so a later prompt is allowed")
	}
}

func TestAuthGiveUp_TTLPrunesStaleEntries(t *testing.T) {
	agent, _, _ := newTestAgent()

	staleKey := authFlightKey("u-old", "trace-old", "cred")
	agent.authInFlightMu.Lock()
	agent.authFailures[staleKey] = &authFailureState{count: 5, updated: time.Now().Add(-2 * authFailureTTL)}
	agent.authInFlightMu.Unlock()

	agent.recordAuthFailure("u1", "trace-1", "cred")

	agent.authInFlightMu.Lock()
	_, stillThere := agent.authFailures[staleKey]
	live := agent.authFailures[authFlightKey("u1", "trace-1", "cred")]
	agent.authInFlightMu.Unlock()
	if stillThere {
		t.Error("an entry older than authFailureTTL must be pruned to avoid unbounded growth")
	}
	if live == nil || live.count != 1 {
		t.Errorf("live entry should survive the prune with count 1, got %+v", live)
	}
}

func TestAuthGiveUp_TimeoutsAccumulateAcrossTasksInTrace(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", stubPendingAuthWithCred("cred-x"))
	agent.SetAuthPendingTimeout(40 * time.Millisecond)
	agent.SetAuthMaxAttempts(2)

	fireTimeout := func(taskID, callID string) {
		t.Helper()
		before := resumeCalls.Load()
		state := newTestTaskState(agent)
		state.TaskID = taskID
		state.UserProps.TraceID = "trace-shared"
		pca := defaultPauseCallArgs(agent, state)
		if _, paused := agent.dispatchToolCallsMayPause(
			t.Context(), state,
			singleCall("stubtool", callID, "{}"),
			pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
		); !paused {
			t.Fatal("expected pause on pending-auth")
		}
		deadline := time.After(2 * time.Second)
		for resumeCalls.Load() == before {
			select {
			case <-deadline:
				t.Fatal("pending-auth timeout did not fire within 2s")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	fireTimeout("task-1", "call-1")
	if agent.authGaveUp("u-test", "trace-shared", "cred-x") {
		t.Fatal("after one timeout the flow must NOT be given up (cap 2)")
	}
	fireTimeout("task-2", "call-2")
	if !agent.authGaveUp("u-test", "trace-shared", "cred-x") {
		t.Error("after two timeouts in the same trace the flow must be given up, ending the loop")
	}
}
