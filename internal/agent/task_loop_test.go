// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestClassifyPendingKind_AuthMarker(t *testing.T) {
	got := classifyPendingKind(toolauth.NewPendingAuthResult("call-1", nil, ""))
	if got != PendingCallAuth {
		t.Errorf("classifyPendingKind(auth-pending) = %v, want PendingCallAuth", got)
	}
}

func TestClassifyPendingKind_HILApproval(t *testing.T) {
	t.Parallel()
	hilPending := &tool.Result{
		Status: tool.StatusPending,
		Data: map[string]any{
			"pending_kind":   "hil_approval",
			"hil_request_id": "hil-abc123",
			"hil_timeout_ns": float64(5 * 60 * 1e9),
		},
	}
	got := classifyPendingKind(hilPending)
	if got != PendingCallHILApproval {
		t.Errorf("classifyPendingKind(hil_approval) = %v, want PendingCallHILApproval", got)
	}
}

func TestClassifyPendingKind_PeerDefault(t *testing.T) {
	plainPending := &tool.Result{
		Status: tool.StatusPending,
		Data:   map[string]any{"sub_task_id": "st-1"},
	}
	got := classifyPendingKind(plainPending)
	if got != PendingCallPeer {
		t.Errorf("classifyPendingKind(plain-pending) = %v, want PendingCallPeer", got)
	}
}

func TestClassifyPendingKindTrustsARemoteToolMarkerOnlyFromTheInProcessCallback(t *testing.T) {
	t.Parallel()
	data := map[string]any{"pending_kind": "remote_tool", "sub_task_id": "st-1"}
	for _, tc := range []struct {
		name   string
		result *tool.Result
		want   PendingCallKind
	}{
		{"a result carrying the delivery callback is a remote tool pause", &tool.Result{Status: tool.StatusPending, Data: data, OnDelivery: func(func(*tool.Result)) {}}, PendingCallRemoteTool},
		{"a result claiming the kind without a callback is a plain pending call", &tool.Result{Status: tool.StatusPending, Data: data}, PendingCallPeer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyPendingKind(tc.result); got != tc.want {
				t.Errorf("classifyPendingKind(%+v) = %v, want %v", tc.result, got, tc.want)
			}
		})
	}
}

func TestDispatchToolSingle_ExecutesWithoutPauseRegistration(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", func(ctx context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed"), nil
	})
	state := newTestTaskState(agent)
	state.PendingCalls["call-r1"] = &PendingToolCall{
		ToolCallID: "call-r1",
		ToolName:   "stubtool",
		Kind:       PendingCallAuth,
		Index:      0,
	}

	result, err := agent.dispatchToolSingle(t.Context(), state, state.PendingCalls["call-r1"])
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result == nil || result.Status != tool.StatusSuccess {
		t.Errorf("result = %+v, want StatusSuccess", result)
	}
	if state.PendingCalls["call-r1"] == nil {
		t.Error("dispatchToolSingle should not remove the pending entry — caller owns lifecycle")
	}
}

type resolvedEffectStub struct {
	*testBuiltinTool
	seen []tool.EffectClass
}

func (s *resolvedEffectStub) EffectClass() tool.EffectClass {
	return tool.DeclaredEffect(tool.EffectReadOnly)
}

func (s *resolvedEffectStub) ExecuteResolved(_ context.Context, _ map[string]any, _ *tool.Context, effect tool.EffectClass) (*tool.Result, error) {
	s.seen = append(s.seen, effect)
	return tool.OK("replayed"), nil
}

func TestDispatchToolSingle_AnOperatorSideEffectingOverrideSurvivesTheAuthReplay(t *testing.T) {
	agent, _, _ := newTestAgent()
	stub := &resolvedEffectStub{testBuiltinTool: &testBuiltinTool{name: "reader", description: "reads"}}
	if err := agent.Tools().Register(stub); err != nil {
		t.Fatal(err)
	}
	override := tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}
	state := newTestTaskState(agent)
	call := &PendingToolCall{ToolCallID: "call-e1", ToolName: "reader", Kind: PendingCallAuth}

	if _, err := agent.dispatchToolSingle(t.Context(), state, call); err != nil {
		t.Fatalf("replay without override: %v", err)
	}
	agent.Tools().SetEffectOverrides(map[string]tool.EffectClass{"reader": override})
	if _, err := agent.dispatchToolSingle(t.Context(), state, call); err != nil {
		t.Fatalf("replay with override: %v", err)
	}

	want := []tool.EffectClass{tool.DeclaredEffect(tool.EffectReadOnly), override}
	if len(stub.seen) != 2 || stub.seen[0] != want[0] || stub.seen[1] != want[1] {
		t.Errorf("effects handed to the tool on replay = %+v, want %+v", stub.seen, want)
	}
}

func TestDispatchToolSingle_RaisesPendingAuth_DoesNotRegister(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-r2"))
	state := newTestTaskState(agent)
	state.PendingCalls["call-r2"] = &PendingToolCall{
		ToolCallID: "call-r2",
		ToolName:   "stubtool",
		Kind:       PendingCallAuth,
		Index:      0,
	}

	result, err := agent.dispatchToolSingle(t.Context(), state, state.PendingCalls["call-r2"])
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !toolauth.IsPendingAuthResult(result) {
		t.Errorf("result = %+v, want pending-auth", result)
	}
	if len(state.PendingCalls) != 1 {
		t.Errorf("PendingCalls len = %d, want 1 (replay must not re-register)", len(state.PendingCalls))
	}
}

func TestDispatchToolSingle_DoesNotPublishStatusEvents(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("re-executed"), nil
	})
	brk := newMockBroker()
	agent.svc.(*mockServices).brk = brk

	state := newTestTaskState(agent)
	state.UserProps.A2AStatusTopic = "test-ns/a2a/v1/agent/status/test-agent"
	state.PendingCalls["call-dup"] = &PendingToolCall{
		ToolCallID: "call-dup",
		ToolName:   "stubtool",
		Kind:       PendingCallAuth,
		Index:      0,
	}

	_, err := agent.dispatchToolSingle(t.Context(), state, state.PendingCalls["call-dup"])
	if err != nil {
		t.Fatalf("err = %v", err)
	}

	for _, msg := range brk.getPublished() {
		payload := string(msg.payload)
		if strings.Contains(payload, "call-dup") && (strings.Contains(payload, "tool_invocation_start") || strings.Contains(payload, "tool_result")) {
			t.Errorf("dispatchToolSingle published a status event (would double-publish on resume): topic=%s payload=%s", msg.topic, payload)
		}
	}
}

func TestDispatcher_PendingAuth_CapturesOriginalArgs(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "stubtool", stubReturnsPendingAuth("call-xyz"))
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	_, paused := agent.dispatchToolCallsMayPause(
		t.Context(), state,
		singleCall("stubtool", "call-xyz", `{"x": 1, "y": "hello"}`),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger,
	)
	if !paused {
		t.Fatal("expected paused=true")
	}
	pc := state.PendingCalls["call-xyz"]
	if pc == nil {
		t.Fatal("no pending call registered")
	}
	if pc.Kind != PendingCallAuth {
		t.Errorf("Kind = %v, want PendingCallAuth", pc.Kind)
	}
	if got := pc.OriginalArgs["x"]; got != 1 && got != float64(1) {
		t.Errorf("OriginalArgs[x] = %v (%T), want 1", got, got)
	}
	if got := pc.OriginalArgs["y"]; got != "hello" {
		t.Errorf("OriginalArgs[y] = %v, want hello", got)
	}
}

func TestBuildApprovalResult_Answered(t *testing.T) {
	t.Parallel()
	resp := &a2a.UserInputResponseData{
		RequestID: "req-1",
		Status:    "answered",
		Data:      map[string]any{"decision": "approved", "approver": "alice"},
	}
	result := buildApprovalResultFromHILResponse(slog.Default(), resp, 10*time.Minute)
	if result.Status != tool.StatusSuccess {
		t.Fatalf("Status = %v, want StatusSuccess", result.Status)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Message), &data); err != nil {
		t.Fatalf("failed to unmarshal result message: %v", err)
	}
	if data["status"] != "approved" {
		t.Errorf("status = %v, want approved", data["status"])
	}
	if data["approver"] != "alice" {
		t.Errorf("approver = %v, want alice", data["approver"])
	}
}

func TestBuildApprovalResult_Answered_MissingDecision(t *testing.T) {
	t.Parallel()
	resp := &a2a.UserInputResponseData{
		RequestID: "req-1",
		Status:    "answered",
		Data:      map[string]any{},
	}
	result := buildApprovalResultFromHILResponse(slog.Default(), resp, 10*time.Minute)
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Message), &data); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if data["status"] != "unknown" {
		t.Errorf("status = %v, want unknown (fallback for missing decision)", data["status"])
	}
}

func TestBuildApprovalResult_TimedOut(t *testing.T) {
	t.Parallel()
	resp := &a2a.UserInputResponseData{
		RequestID: "req-2",
		Status:    "timed_out",
		Data:      map[string]any{},
	}
	result := buildApprovalResultFromHILResponse(slog.Default(), resp, 30*time.Minute)
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Message), &data); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if data["status"] != "timed_out" {
		t.Errorf("status = %v, want timed_out", data["status"])
	}
	msg, _ := data["message"].(string)
	if !strings.Contains(msg, "30 minutes") {
		t.Errorf("message = %q, want to contain '30 minutes'", msg)
	}
}

func TestBuildApprovalResult_Cancelled(t *testing.T) {
	t.Parallel()
	resp := &a2a.UserInputResponseData{
		RequestID: "req-3",
		Status:    "cancelled",
		Data:      map[string]any{},
	}
	result := buildApprovalResultFromHILResponse(slog.Default(), resp, 10*time.Minute)
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Message), &data); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if data["status"] != "cancelled" {
		t.Errorf("status = %v, want cancelled", data["status"])
	}
}

func TestBuildApprovalResult_UnknownStatus(t *testing.T) {
	t.Parallel()
	resp := &a2a.UserInputResponseData{
		RequestID: "req-4",
		Status:    "weird_status",
		Data:      map[string]any{},
	}
	result := buildApprovalResultFromHILResponse(slog.Default(), resp, 10*time.Minute)
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Message), &data); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if data["status"] != "weird_status" {
		t.Errorf("status = %v, want weird_status (passthrough)", data["status"])
	}
}

func TestDispatchToolCallsMayPause_TagsAuthPendingWithCredentialKey(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "authtool", func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return toolauth.NewPendingAuthFollowerResult("call-1", args, "cred-X"), nil
	})
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	_, paused := agent.dispatchToolCallsMayPause(t.Context(), state, singleCall("authtool", "call-1", `{}`), pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger)
	if !paused {
		t.Fatal("expected pause on pending-auth result")
	}
	pc := state.PendingCalls["call-1"]
	if pc == nil || pc.Kind != PendingCallAuth {
		t.Fatalf("expected PendingCallAuth, got %+v", pc)
	}
	if pc.CredentialKey != "cred-X" {
		t.Errorf("pc.CredentialKey = %q, want cred-X", pc.CredentialKey)
	}
	if !pc.IsAuthFollower {
		t.Error("pc.IsAuthFollower should be true for a follower pending result")
	}
}
