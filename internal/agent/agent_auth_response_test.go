// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestHandleAuthResponse_HappyPath_ResumesPausedTask(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed ok"), nil
	})

	state := newTestTaskState(agent)
	state.TaskID = "task-abc"
	state.UserProps.UserID = "u1"
	state.TotalPending = 1
	state.PendingCalls["call-1"] = &PendingToolCall{
		ToolCallID:   "call-1",
		ToolName:     "stubtool",
		Kind:         PendingCallAuth,
		Index:        0,
		OriginalArgs: map[string]any{"q": "test"},
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-1", "stubtool", `{"q":"test"}`)
	agent.pausedTasks.Store(state)

	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-abc", "call-1", "test-key")
	got, err := agent.handleAuthResponse(t.Context(), "task-abc", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("want nil response (task resumed in-place), got %+v", got)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.PendingCalls["call-1"]; ok {
		t.Error("pending call should be deleted after resume")
	}
	if state.SyncResults[0] == nil || state.SyncResults[0].Status != tool.StatusSuccess {
		t.Errorf("SyncResults[0] = %+v, want success", state.SyncResults[0])
	}
	if resumeCalls.Load() != 1 {
		t.Errorf("resumeTaskHook called %d times, want 1", resumeCalls.Load())
	}
}

func TestHandleAuthResponse_F3_ExchangeFailure_ResumesWithError(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	agent := newTestAgentWithStubTool(t, "stubtool", alwaysOKTool())
	state := pausedStateWithAuthEntry(t, agent, "task-f3", "call-f3")

	registerFailingAuthHandler(t, agent, "test-key", errors.New("oauth2: invalid_grant"))

	resp := &a2a.AuthResponseData{
		TargetAgent:       agent.cfg.Name,
		TargetAgentTaskID: "task-f3",
		FunctionCallID:    "call-f3",
		CredentialKey:     "test-key",
		Code:              "bad-code",
	}

	got, err := agent.handleAuthResponse(t.Context(), "task-f3", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("want nil response (task resumed in-place), got %+v", got)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.PendingCalls["call-f3"]; ok {
		t.Error("pending call should be deleted")
	}
	r := state.SyncResults[0]
	if r == nil || r.Status != tool.StatusError {
		t.Errorf("SyncResults[0] = %+v, want error", r)
	}
	if !strings.Contains(r.Message, "invalid_grant") {
		t.Errorf("Message = %q, want to contain invalid_grant", r.Message)
	}
}

func TestHandleAuthResponse_F5_FirstReretry_LeavesPendingInPlace(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {
		t.Error("resume should NOT fire on first retry — task is still pending")
	})

	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-f5"))
	state := pausedStateWithAuthEntry(t, agent, "task-f5", "call-f5")
	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-f5", "call-f5", "test-key")
	got, err := agent.handleAuthResponse(t.Context(), "task-f5", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %+v", got)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	pc := state.PendingCalls["call-f5"]
	if pc == nil {
		t.Fatal("pending call should still be registered after first retry")
	}
	if pc.AuthRetries != 1 {
		t.Errorf("AuthRetries = %d, want 1", pc.AuthRetries)
	}
}

func TestHandleAuthResponse_F5_SecondRetry_ResolvesWithError(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-f5b"))
	state := pausedStateWithAuthEntry(t, agent, "task-f5b", "call-f5b")
	state.PendingCalls["call-f5b"].AuthRetries = 1
	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-f5b", "call-f5b", "test-key")
	_, err := agent.handleAuthResponse(t.Context(), "task-f5b", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.PendingCalls["call-f5b"]; ok {
		t.Error("pending call should be deleted after 2nd retry")
	}
	r := state.SyncResults[0]
	if r == nil || r.Status != tool.StatusError {
		t.Errorf("SyncResults[0] = %+v, want error", r)
	}
	if !strings.Contains(r.Message, "after retry") && !strings.Contains(r.Message, "could not be completed") {
		t.Errorf("Message = %q, want to indicate retry exhaustion", r.Message)
	}
	if resumeCalls.Load() != 1 {
		t.Errorf("resumeTaskHook called %d times, want 1", resumeCalls.Load())
	}
}

func TestHandleAuthResponse_RetryExhausted_PreservesToolMessage(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	const reason = "atlassian_rest_request: 401 from Atlassian — scope does not match"
	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return toolauth.WithToolMessage(toolauth.NewPendingAuthResult("call-scope", args, ""), reason), nil
	})
	state := pausedStateWithAuthEntry(t, agent, "task-scope", "call-scope")
	state.PendingCalls["call-scope"].AuthRetries = 1
	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-scope", "call-scope", "test-key")
	if _, err := agent.handleAuthResponse(t.Context(), "task-scope", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("err = %v", err)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	r := state.SyncResults[0]
	if r == nil || r.Status != tool.StatusError {
		t.Fatalf("SyncResults[0] = %+v, want error", r)
	}
	if !strings.Contains(r.Message, "after retry") && !strings.Contains(r.Message, "could not be completed") {
		t.Errorf("Message = %q, want to indicate retry exhaustion", r.Message)
	}
	if !strings.Contains(r.Message, "scope does not match") {
		t.Errorf("Message = %q, want to contain the underlying tool reason", r.Message)
	}
}

func TestHandleAuthResponse_F8_DuplicateDelivery_Idempotent(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	var execCount atomic.Int32
	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		execCount.Add(1)
		return tool.OK("re-executed"), nil
	})
	_ = pausedStateWithAuthEntry(t, agent, "task-f8", "call-f8")
	registerStubAuthHandler(t, agent, "test-key")

	resp := authResp("task-f8", "call-f8", "test-key")

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_, _ = agent.handleAuthResponse(t.Context(), "task-f8", resp, a2a.UserProperties{UserID: "u1"})
		})
	}
	wg.Wait()

	if got := execCount.Load(); got != 1 {
		t.Errorf("tool executed %d times, want 1 (duplicate deliveries must be idempotent)", got)
	}
}

func TestHandleAuthResponse_F8_SecondDelivery_ExchangeFails_ReturnsLegacyResponse(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	agent := newTestAgentWithStubTool(t, "stubtool", alwaysOKTool())
	_ = pausedStateWithAuthEntry(t, agent, "task-f8b", "call-f8b")

	var callCount atomic.Int32
	agent.RegisterAuthHandler(&stubAuthHandler{
		key: "test-key",
		handleFn: func(_ context.Context, _ *a2a.AuthResponseData, _ *tool.Context) error {
			if callCount.Add(1) > 1 {
				return errors.New("oauth2: invalid_grant")
			}
			return nil
		},
	})

	resp := authResp("task-f8b", "call-f8b", "test-key")

	got1, err := agent.handleAuthResponse(t.Context(), "task-f8b", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("first delivery err = %v", err)
	}
	if got1 != nil {
		t.Errorf("first delivery returned %+v, want nil (task resumed in-place)", got1)
	}

	got2, err := agent.handleAuthResponse(t.Context(), "task-f8b", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("second delivery err = %v", err)
	}
	if got2 == nil {
		t.Fatal("second delivery returned nil, want legacy saved response")
	}
	if got2.Error != nil {
		t.Errorf("second delivery returned standalone error %+v; want legacy saved response without error", got2.Error)
	}
}

func TestHandleAuthResponse_ResumesCoalescedFollowerInOtherTask(t *testing.T) {
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

	if _, ok := resumed.Load("task-leader"); !ok {
		t.Error("leader task should have resumed")
	}
	if _, ok := resumed.Load("task-follower"); !ok {
		t.Error("follower task should have resumed via the sweep")
	}
	follower.mu.Lock()
	if _, ok := follower.PendingCalls["call-F"]; ok {
		t.Error("follower pending call should be claimed")
	}
	if follower.SyncResults[0] == nil || follower.SyncResults[0].Status != tool.StatusSuccess {
		t.Errorf("follower SyncResults[0] = %+v, want success", follower.SyncResults[0])
	}
	follower.mu.Unlock()

	if !agent.beginAuthFlow("u1", "", "cred-X") {
		t.Error("in-flight flag should be cleared after the flow resolved")
	}
}

func TestHandleAuthResponse_DoesNotResumeFollowerInDifferentTraceID(t *testing.T) {
	var resumed sync.Map
	useResumeHook(t, func(_ *Agent, s *TaskExecutionState) { resumed.Store(s.TaskID, struct{}{}) })

	agent := newTestAgentWithStubTool(t, "authtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed ok"), nil
	})
	registerStubAuthHandler(t, agent, "cred-X")

	leader := newTestTaskState(agent)
	leader.TaskID = "task-leader"
	leader.UserProps.UserID = "u1"
	leader.UserProps.TraceID = "trace-1"
	leader.TotalPending = 1
	leader.PendingCalls["call-L"] = &PendingToolCall{ToolCallID: "call-L", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", OriginalArgs: map[string]any{}}
	leader.PausedAssistantMsg = assistantMsgWithOneToolCall("call-L", "authtool", `{}`)
	agent.pausedTasks.Store(leader)

	sameReq := newTestTaskState(agent)
	sameReq.TaskID = "task-same-req"
	sameReq.UserProps.UserID = "u1"
	sameReq.UserProps.TraceID = "trace-1"
	sameReq.TotalPending = 1
	sameReq.PendingCalls["call-S"] = &PendingToolCall{ToolCallID: "call-S", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	sameReq.PausedAssistantMsg = assistantMsgWithOneToolCall("call-S", "authtool", `{}`)
	agent.pausedTasks.Store(sameReq)

	otherReq := newTestTaskState(agent)
	otherReq.TaskID = "task-other-req"
	otherReq.UserProps.UserID = "u1"
	otherReq.UserProps.TraceID = "trace-2"
	otherReq.TotalPending = 1
	otherReq.PendingCalls["call-O"] = &PendingToolCall{ToolCallID: "call-O", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	otherReq.PausedAssistantMsg = assistantMsgWithOneToolCall("call-O", "authtool", `{}`)
	agent.pausedTasks.Store(otherReq)

	agent.beginAuthFlow("u1", "trace-1", "cred-X")
	agent.beginAuthFlow("u1", "trace-2", "cred-X")

	resp := authResp("task-leader", "call-L", "cred-X")
	if _, err := agent.handleAuthResponse(t.Context(), "task-leader", resp, a2a.UserProperties{UserID: "u1", TraceID: "trace-1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	if _, ok := resumed.Load("task-same-req"); !ok {
		t.Error("same-traceID follower should have resumed via the sweep")
	}
	if _, ok := resumed.Load("task-other-req"); ok {
		t.Error("a follower in a DIFFERENT traceID must NOT be resumed by this request's flow")
	}
	otherReq.mu.Lock()
	if _, ok := otherReq.PendingCalls["call-O"]; !ok {
		t.Error("different-traceID follower's pending call must remain (awaiting its own prompt)")
	}
	otherReq.mu.Unlock()
	if agent.beginAuthFlow("u1", "trace-2", "cred-X") {
		t.Error("the independent request's in-flight marker must not be cleared by this flow")
	}
}

func TestHandleAuthResponse_ResumesSameTaskFollower(t *testing.T) {
	var resumed sync.Map
	useResumeHook(t, func(_ *Agent, s *TaskExecutionState) { resumed.Store(s.TaskID, struct{}{}) })

	agent := newTestAgentWithStubTool(t, "authtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed ok"), nil
	})
	registerStubAuthHandler(t, agent, "cred-X")

	st := newTestTaskState(agent)
	st.TaskID = "task-1"
	st.UserProps.UserID = "u1"
	st.TotalPending = 2
	st.PendingCalls["call-L"] = &PendingToolCall{ToolCallID: "call-L", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", OriginalArgs: map[string]any{}}
	st.PendingCalls["call-F"] = &PendingToolCall{ToolCallID: "call-F", ToolName: "authtool", Kind: PendingCallAuth, Index: 1, CredentialKey: "cred-X", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	st.PausedAssistantMsg = assistantMsgWithOneToolCall("call-L", "authtool", `{}`)
	agent.pausedTasks.Store(st)
	agent.beginAuthFlow("u1", "", "cred-X")

	resp := authResp("task-1", "call-L", "cred-X")
	if _, err := agent.handleAuthResponse(t.Context(), "task-1", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	st.mu.Lock()
	if _, ok := st.PendingCalls["call-F"]; ok {
		t.Error("same-task follower call-F must be claimed, not left pending")
	}
	if st.SyncResults[0] == nil || st.SyncResults[0].Status != tool.StatusSuccess {
		t.Errorf("leader SyncResults[0] = %+v, want success", st.SyncResults[0])
	}
	if st.SyncResults[1] == nil || st.SyncResults[1].Status != tool.StatusSuccess {
		t.Errorf("follower SyncResults[1] = %+v, want success", st.SyncResults[1])
	}
	st.mu.Unlock()
	if _, ok := resumed.Load("task-1"); !ok {
		t.Error("task must resume once both pending calls resolve (TotalPending hit 0)")
	}
	if atomic.LoadInt32(&st.TotalPending) != 0 {
		t.Errorf("TotalPending = %d, want 0", atomic.LoadInt32(&st.TotalPending))
	}
}

func TestHandleAuthResponse_StaleToken_LeaderRePromptsNotHangs(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {
		t.Error("resume must NOT fire: the re-dispatch re-prompted and re-pended the call")
	})

	var signalCount atomic.Int32
	var credentialReady atomic.Bool
	agent := newTestAgentWithStubTool(t, "authtool",
		coalesceAuthExecFn("cred-stale", &signalCount, &credentialReady))
	registerStubAuthHandler(t, agent, "cred-stale")

	state := newTestTaskState(agent)
	state.TaskID = "task-stale"
	state.UserProps.UserID = "u1"
	state.TotalPending = 1
	state.PendingCalls["call-S"] = &PendingToolCall{
		ToolCallID:    "call-S",
		ToolName:      "authtool",
		Kind:          PendingCallAuth,
		Index:         0,
		CredentialKey: "cred-stale",
		OriginalArgs:  map[string]any{},
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-S", "authtool", `{}`)
	agent.pausedTasks.Store(state)
	agent.beginAuthFlow("u1", "", "cred-stale")

	resp := authResp("task-stale", "call-S", "cred-stale")
	got, err := agent.handleAuthResponse(t.Context(), "task-stale", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}
	if got != nil {
		t.Errorf("want nil response (call re-pended in-place), got %+v", got)
	}

	if n := signalCount.Load(); n != 1 {
		t.Errorf("authentication_required signals from re-dispatch = %d, want 1 (stale-token re-dispatch must re-prompt, not coalesce as a leaderless follower)", n)
	}

	state.mu.Lock()
	if _, ok := state.PendingCalls["call-S"]; !ok {
		t.Error("call should be re-pended in PendingCalls after the stale-token re-prompt")
	}
	state.mu.Unlock()
	if n := atomic.LoadInt32(&state.TotalPending); n != 1 {
		t.Errorf("TotalPending = %d, want 1 (call still waiting on the fresh prompt)", n)
	}
}

func TestHandleAuthResponse_F6_LeaderGone_StillResumesFollower(t *testing.T) {
	var resumed sync.Map
	useResumeHook(t, func(_ *Agent, s *TaskExecutionState) { resumed.Store(s.TaskID, struct{}{}) })

	agent := newTestAgentWithStubTool(t, "authtool", alwaysOKTool())
	registerStubAuthHandler(t, agent, "cred-X")

	follower := newTestTaskState(agent)
	follower.TaskID = "task-follower"
	follower.UserProps.UserID = "u1"
	follower.TotalPending = 1
	follower.PendingCalls["call-F"] = &PendingToolCall{ToolCallID: "call-F", ToolName: "authtool", Kind: PendingCallAuth, Index: 0, CredentialKey: "cred-X", IsAuthFollower: true, OriginalArgs: map[string]any{}}
	follower.PausedAssistantMsg = assistantMsgWithOneToolCall("call-F", "authtool", `{}`)
	agent.pausedTasks.Store(follower)
	agent.beginAuthFlow("u1", "", "cred-X")

	resp := authResp("task-leader-gone", "call-L", "cred-X")
	if _, err := agent.handleAuthResponse(t.Context(), "task-leader-gone", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}
	if _, ok := resumed.Load("task-follower"); !ok {
		t.Error("follower should resume even though the leader task was gone (F6 sweep)")
	}
}

func TestHandleAuthResponse_TwoLeaderConcurrent_NoDeadlock(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	agent := newTestAgentWithStubTool(t, "authtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed ok"), nil
	})
	registerStubAuthHandler(t, agent, "cred1")
	registerStubAuthHandler(t, agent, "cred2")

	mkPC := func(callID, credKey string, idx int, follower bool) *PendingToolCall {
		return &PendingToolCall{
			ToolCallID:     callID,
			ToolName:       "authtool",
			Kind:           PendingCallAuth,
			Index:          idx,
			CredentialKey:  credKey,
			IsAuthFollower: follower,
			OriginalArgs:   map[string]any{},
		}
	}

	taskA := newTestTaskState(agent)
	taskA.TaskID = "taskA"
	taskA.UserProps.UserID = "u1"
	taskA.TotalPending = 2
	taskA.PendingCalls["A1"] = mkPC("A1", "cred1", 0, false)
	taskA.PendingCalls["A2"] = mkPC("A2", "cred2", 1, true)
	taskA.PausedAssistantMsg = assistantMsgWithOneToolCall("A1", "authtool", `{}`)
	agent.pausedTasks.Store(taskA)

	taskB := newTestTaskState(agent)
	taskB.TaskID = "taskB"
	taskB.UserProps.UserID = "u1"
	taskB.TotalPending = 2
	taskB.PendingCalls["B1"] = mkPC("B1", "cred2", 0, false)
	taskB.PendingCalls["B2"] = mkPC("B2", "cred1", 1, true)
	taskB.PausedAssistantMsg = assistantMsgWithOneToolCall("B1", "authtool", `{}`)
	agent.pausedTasks.Store(taskB)

	agent.beginAuthFlow("u1", "", "cred1")
	agent.beginAuthFlow("u1", "", "cred2")

	userProps := a2a.UserProperties{UserID: "u1"}
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = agent.handleAuthResponse(t.Context(), "taskA", authResp("taskA", "A1", "cred1"), userProps)
	})
	wg.Go(func() {
		_, _ = agent.handleAuthResponse(t.Context(), "taskB", authResp("taskB", "B1", "cred2"), userProps)
	})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: handleAuthResponse did not return within 10s")
	}

	for _, st := range []*TaskExecutionState{taskA, taskB} {
		st.mu.Lock()
		if len(st.PendingCalls) != 0 {
			t.Errorf("%s: %d pending calls left, want 0", st.TaskID, len(st.PendingCalls))
		}
		for idx := range 2 {
			r := st.SyncResults[idx]
			if r == nil || r.Status != tool.StatusSuccess {
				t.Errorf("%s: SyncResults[%d] = %+v, want success", st.TaskID, idx, r)
			}
		}
		st.mu.Unlock()
		if n := atomic.LoadInt32(&st.TotalPending); n != 0 {
			t.Errorf("%s: TotalPending = %d, want 0", st.TaskID, n)
		}
	}

	if !agent.beginAuthFlow("u1", "", "cred1") {
		t.Error("cred1 in-flight flag should be cleared")
	}
	if !agent.beginAuthFlow("u1", "", "cred2") {
		t.Error("cred2 in-flight flag should be cleared")
	}
}

func TestHandleAuthResponse_SweepCapExhaust_ClearsInFlightFlag(t *testing.T) {
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) {})

	var signalCount atomic.Int32
	var credentialReady atomic.Bool
	agent := newTestAgentWithStubTool(t, "authtool",
		coalesceAuthExecFn("cred-X", &signalCount, &credentialReady))
	registerStubAuthHandler(t, agent, "cred-X")

	follower := newTestTaskState(agent)
	follower.TaskID = "task-follower"
	follower.UserProps.UserID = "u1"
	follower.TotalPending = 1
	follower.PendingCalls["call-F"] = &PendingToolCall{
		ToolCallID:     "call-F",
		ToolName:       "authtool",
		Kind:           PendingCallAuth,
		Index:          0,
		CredentialKey:  "cred-X",
		IsAuthFollower: true,
		AuthRetries:    1,
		OriginalArgs:   map[string]any{},
	}
	follower.PausedAssistantMsg = assistantMsgWithOneToolCall("call-F", "authtool", `{}`)
	agent.pausedTasks.Store(follower)
	agent.beginAuthFlow("u1", "", "cred-X")

	resp := authResp("task-leader-gone", "call-L", "cred-X")
	if _, err := agent.handleAuthResponse(t.Context(), "task-leader-gone", resp, a2a.UserProperties{UserID: "u1"}); err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}

	follower.mu.Lock()
	if _, ok := follower.PendingCalls["call-F"]; ok {
		t.Error("follower call should be resolved (not left pending) after cap exhaust")
	}
	r := follower.SyncResults[0]
	if r == nil || r.Status != tool.StatusError {
		t.Errorf("follower SyncResults[0] = %+v, want error", r)
	}
	follower.mu.Unlock()

	if !agent.beginAuthFlow("u1", "", "cred-X") {
		t.Error("in-flight flag must be cleared after cap-exhaust resolves the follower as error")
	}
}

func TestHandlePendingAuthTimeout_SetsErrorAndResumes(t *testing.T) {
	var resumeCalls atomic.Int32
	useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumeCalls.Add(1) })

	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("ok"), nil
	})

	state := newTestTaskState(agent)
	state.TaskID = "task-timeout"
	state.TotalPending = 1
	state.PendingCalls["call-t"] = &PendingToolCall{
		ToolCallID: "call-t",
		ToolName:   "stubtool",
		Kind:       PendingCallAuth,
		Index:      0,
		Timeout:    10 * time.Minute,
	}
	agent.pausedTasks.Store(state)

	agent.handlePendingAuthTimeout("task-timeout", "call-t")

	state.mu.Lock()
	defer state.mu.Unlock()

	if _, ok := state.PendingCalls["call-t"]; ok {
		t.Error("pending call should be removed after timeout")
	}
	r := state.SyncResults[0]
	if r == nil || r.Status != tool.StatusError {
		t.Fatalf("SyncResults[0] = %+v, want error", r)
	}
	if !strings.Contains(r.Message, "timed out") {
		t.Errorf("message = %q, want 'timed out'", r.Message)
	}
	if !strings.Contains(r.Message, "Please try") {
		t.Errorf("message = %q, want retry guidance", r.Message)
	}
	if resumeCalls.Load() != 1 {
		t.Errorf("resumeTaskHook called %d times, want 1", resumeCalls.Load())
	}
}
