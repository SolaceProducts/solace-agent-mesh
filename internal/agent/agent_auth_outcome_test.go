// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestHandleAuthResponse_AuthSucceeds_ToolBusinessError_RecordsSuccess(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.Error("upstream returned 404"), nil
	})
	state := pausedStateWithAuthEntry(t, agent, "task-biz", "call-biz")
	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-biz", "call-biz", "test-key")
	if _, err := agent.handleAuthResponse(t.Context(), "task-biz", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.SyncResults[0] == nil || state.SyncResults[0].Status != tool.StatusError {
		t.Fatalf("SyncResults[0] = %+v, want a business error result", state.SyncResults[0])
	}
	if state.pendingAuthOutcome == nil {
		t.Fatal("expected an auth outcome to be recorded")
	}
	if !state.pendingAuthOutcome.success {
		t.Error("auth outcome success = false; auth succeeded, only the tool's own call errored")
	}
}

func TestHandleAuthResponse_ExchangeFailure_RecordsFailure(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	agent := newTestAgentWithStubTool(t, "stubtool", alwaysOKTool())
	state := pausedStateWithAuthEntry(t, agent, "task-xf", "call-xf")
	registerFailingAuthHandler(t, agent, "test-key", errors.New("oauth2: invalid_grant"))

	resp := &a2a.AuthResponseData{TargetAgent: agent.cfg.Name, TargetAgentTaskID: "task-xf", FunctionCallID: "call-xf", CredentialKey: "test-key", Code: "bad-code"}
	if _, err := agent.handleAuthResponse(t.Context(), "task-xf", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.pendingAuthOutcome == nil {
		t.Fatal("expected an auth outcome to be recorded")
	}
	if state.pendingAuthOutcome.success {
		t.Error("auth outcome success = true; token exchange failed")
	}
}

func TestHandleAuthResponse_RetryExhausted_RecordsFailure(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-rx"))
	state := pausedStateWithAuthEntry(t, agent, "task-rx", "call-rx")
	state.PendingCalls["call-rx"].AuthRetries = 1
	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-rx", "call-rx", "test-key")
	if _, err := agent.handleAuthResponse(t.Context(), "task-rx", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.pendingAuthOutcome == nil {
		t.Fatal("expected an auth outcome to be recorded")
	}
	if state.pendingAuthOutcome.success {
		t.Error("auth outcome success = true; auth retries were exhausted")
	}
}

func TestResumeFollowersInTask_RecordsOutcome(t *testing.T) {
	var resumed sync.Map
	useResumeHook(t, func(_ *Agent, s *TaskExecutionState) { resumed.Store(s.TaskID, struct{}{}) })

	agent := newTestAgentWithStubTool(t, "authtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed ok"), nil
	})
	registerStubAuthHandler(t, agent, "cred-X")

	leader := newTestTaskState(agent)
	leader.TaskID = "task-leader"
	leader.UserProps.UserID = "u1"
	leader.TotalPending = 1
	leader.PendingCalls["call-L"] = &PendingToolCall{ToolCallID: "call-L", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", OriginalArgs: map[string]any{}}
	leader.PausedAssistantMsg = assistantMsgWithOneToolCall("call-L", "authtool", `{}`)
	agent.pausedTasks.Store(leader)

	follower := newTestTaskState(agent)
	follower.TaskID = "task-follower"
	follower.UserProps.UserID = "u1"
	follower.TotalPending = 1
	follower.PendingCalls["call-F"] = &PendingToolCall{ToolCallID: "call-F", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	follower.PausedAssistantMsg = assistantMsgWithOneToolCall("call-F", "authtool", `{}`)
	agent.pausedTasks.Store(follower)

	agent.beginAuthFlow("u1", "", "cred-X")

	resp := authResp("task-leader", "call-L", "cred-X")
	if _, err := agent.handleAuthResponse(t.Context(), "task-leader", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if _, ok := resumed.Load("task-follower"); !ok {
		t.Fatal("follower task should have resumed via the sweep")
	}
	follower.mu.Lock()
	defer follower.mu.Unlock()
	if follower.pendingAuthOutcome == nil {
		t.Fatal("follower resume must record an auth outcome so it emits authentication_result")
	}
	if !follower.pendingAuthOutcome.success {
		t.Error("follower outcome success = false, want true (credential obtained, tool ran ok)")
	}
	if follower.pendingAuthOutcome.functionCallID != "call-F" {
		t.Errorf("follower outcome functionCallID = %q, want call-F", follower.pendingAuthOutcome.functionCallID)
	}
}
