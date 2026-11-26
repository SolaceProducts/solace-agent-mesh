// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync/atomic"
	"testing"
)

func TestCancelLeader_ClearsInFlight_AndFollowerCanRelead(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "authtool", alwaysOKTool())
	registerStubAuthHandler(t, agent, "cred-X")
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	leader := newTestTaskState(agent)
	leader.TaskID = "task-leader"
	leader.UserProps.UserID = "u1"
	leader.TotalPending = 1
	leader.PendingCalls["call-L"] = &PendingToolCall{ToolCallID: "call-L", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X"}
	agent.pausedTasks.Store(leader)
	agent.beginAuthFlow("u1", "", "cred-X")

	agent.cancelPausedTask(leader, cancelFromElsewhere)

	if !agent.beginAuthFlow("u1", "", "cred-X") {
		t.Error("cancelling the leader must clear the in-flight flag so a new leader can take over")
	}
}

func TestPendingAuthTimeout_ClearsInFlight_AndFollowerCanRelead(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "authtool", alwaysOKTool())
	registerStubAuthHandler(t, agent, "cred-Y")
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	leader := newTestTaskState(agent)
	leader.TaskID = "task-leader-timeout"
	leader.UserProps.UserID = "u1"
	leader.TotalPending = 1
	leader.PendingCalls["call-T"] = &PendingToolCall{ToolCallID: "call-T", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-Y"}
	agent.pausedTasks.Store(leader)
	agent.beginAuthFlow("u1", "", "cred-Y")

	agent.handlePendingAuthTimeout(leader.TaskID, "call-T")

	if !agent.beginAuthFlow("u1", "", "cred-Y") {
		t.Error("timing out the leader must clear the in-flight flag so a new leader can take over")
	}
}

func TestCancelLeader_DoesNotRePromptOwnCall(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	var signalCount atomic.Int32
	var credentialReady atomic.Bool
	agent := newTestAgentWithStubTool(t, "authtool",
		coalesceAuthExecFn("cred-Z", &signalCount, &credentialReady))
	registerStubAuthHandler(t, agent, "cred-Z")

	leader := newTestTaskState(agent)
	leader.TaskID = "task-cancel-self"
	leader.UserProps.UserID = "u1"
	leader.TotalPending = 1
	leader.PendingCalls["call-L"] = &PendingToolCall{
		ToolCallID:    "call-L",
		ToolName:      "authtool",
		Kind:          PendingCallAuth,
		Index:         0,
		CredentialKey: "cred-Z",
		OriginalArgs:  map[string]any{},
	}
	leader.PausedAssistantMsg = assistantMsgWithOneToolCall("call-L", "authtool", `{}`)
	agent.pausedTasks.Store(leader)
	agent.beginAuthFlow("u1", "", "cred-Z")

	agent.cancelPausedTask(leader, cancelFromElsewhere)

	if got := signalCount.Load(); got != 0 {
		t.Errorf("authentication_required signals from cancel sweep = %d, want 0 (cancelled task's own call must not be re-prompted)", got)
	}
	if !agent.beginAuthFlow("u1", "", "cred-Z") {
		t.Error("in-flight flag should be cleared after cancel, not re-set by a spurious self re-dispatch")
	}
}

func TestCancelFollower_LeaderInFlightStaysSet(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "authtool", alwaysOKTool())
	registerStubAuthHandler(t, agent, "cred-X")
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	if !agent.beginAuthFlow("u1", "", "cred-X") {
		t.Fatal("beginAuthFlow should succeed for the leader")
	}

	follower := newTestTaskState(agent)
	follower.TaskID = "task-follower"
	follower.UserProps.UserID = "u1"
	follower.TotalPending = 1
	follower.PendingCalls["call-F"] = &PendingToolCall{ToolCallID: "call-F", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	agent.pausedTasks.Store(follower)

	agent.cancelPausedTask(follower, cancelFromElsewhere)

	if agent.beginAuthFlow("u1", "", "cred-X") {
		t.Error("cancelling a follower must NOT clear the leader's in-flight flag")
	}
}

func TestPendingAuthTimeout_Follower_LeaderInFlightStaysSet(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "authtool", alwaysOKTool())
	registerStubAuthHandler(t, agent, "cred-Y")
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	if !agent.beginAuthFlow("u1", "", "cred-Y") {
		t.Fatal("beginAuthFlow should succeed for the leader")
	}

	follower := newTestTaskState(agent)
	follower.TaskID = "task-follower-timeout"
	follower.UserProps.UserID = "u1"
	follower.TotalPending = 1
	follower.PendingCalls["call-FT"] = &PendingToolCall{ToolCallID: "call-FT", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-Y", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	agent.pausedTasks.Store(follower)

	agent.handlePendingAuthTimeout(follower.TaskID, "call-FT")

	if agent.beginAuthFlow("u1", "", "cred-Y") {
		t.Error("timing out a follower must NOT clear the leader's in-flight flag")
	}
}
