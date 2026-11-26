// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type stubExecFn func(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error)

func newTestAgentWithStubTool(t *testing.T, toolName string, execFn stubExecFn) *Agent {
	t.Helper()

	agent, _, _ := newTestAgent()

	if err := agent.Tools().Register(&testBuiltinTool{
		name:        toolName,
		description: "stub tool for auth-resume tests",
		execFn:      execFn,
	}); err != nil {
		t.Fatalf("register stub tool %q: %v", toolName, err)
	}
	return agent
}

func newTestTaskState(agent *Agent) *TaskExecutionState {
	return &TaskExecutionState{
		TaskID:             "test-task",
		ContextID:          "test-ctx",
		OriginalSessionID:  "test-ctx",
		EffectiveSessionID: "test-ctx",
		UserProps: a2a.UserProperties{
			UserID:   "u-test",
			ClientID: "c-test",
		},
		Messages:     []llm.ChatMessage{},
		PendingCalls: map[string]*PendingToolCall{},
		SyncResults:  map[int]*tool.Result{},
		DoneCh:       make(chan *a2a.Response, 1),
		MaxCalls:     agent.cfg.MaxLLMCalls,
	}
}

func stubReturnsPendingAuth(funcCallID string) stubExecFn {
	return func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
		return toolauth.NewPendingAuthResult(funcCallID, args, ""), nil
	}
}

func singleCall(name, callID, argsJSON string) []llm.ToolCall {
	return []llm.ToolCall{{ID: callID, Name: name, Arguments: argsJSON}}
}

type pauseCallArgs struct {
	toolCtx         *tool.Context
	sp              *StatusPublisher
	parallelGroupID string
	sanitizeErrors  []llm.ChatMessage
}

func defaultPauseCallArgs(a *Agent, state *TaskExecutionState) pauseCallArgs {
	return pauseCallArgs{
		toolCtx: &tool.Context{
			AgentName: a.cfg.Name,
			UserID:    state.UserProps.UserID,
			SessionID: state.EffectiveSessionID,
		},
		sp: NewStatusPublisher(a.svc.Broker(), StatusPublisherConfig{
			TaskID:    state.TaskID,
			ContextID: state.ContextID,
			AgentName: a.cfg.Name,
			ClientID:  state.UserProps.ClientID,
			UserID:    state.UserProps.UserID,
		}, slog.Default()),
		parallelGroupID: "",
		sanitizeErrors:  nil,
	}
}

func useResumeHook(t *testing.T, fn func(*Agent, *TaskExecutionState)) {
	t.Helper()
	prev := resumeTaskHook
	resumeTaskHook = fn
	t.Cleanup(func() { resumeTaskHook = prev })
}

type stubAuthHandler struct {
	key      string
	handleFn func(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) error
}

func (s *stubAuthHandler) CredentialKey() string { return s.key }
func (s *stubAuthHandler) HandleAuthResponse(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *tool.Context) error {
	if s.handleFn != nil {
		return s.handleFn(ctx, resp, toolCtx)
	}
	return nil
}

func registerStubAuthHandler(t *testing.T, agent *Agent, key string) {
	t.Helper()
	agent.RegisterAuthHandler(&stubAuthHandler{key: key})
}

func registerFailingAuthHandler(t *testing.T, agent *Agent, key string, err error) {
	t.Helper()
	agent.RegisterAuthHandler(&stubAuthHandler{
		key: key,
		handleFn: func(_ context.Context, _ *a2a.AuthResponseData, _ *tool.Context) error {
			return err
		},
	})
}

func stubReturnsPendingHIL(funcCallID, hilRequestID string) stubExecFn {
	return func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return &tool.Result{
			Status:  tool.StatusPending,
			Message: "Waiting for approval...",
			Data: map[string]any{
				"pending_kind":   "hil_approval",
				"hil_request_id": hilRequestID,
			},
		}, nil
	}
}

func pausedStateWithHILEntry(t *testing.T, agent *Agent, taskID, callID, hilReqID string) *TaskExecutionState {
	t.Helper()
	state := newTestTaskState(agent)
	state.TaskID = taskID
	state.TotalPending = 1
	state.PendingCalls[callID] = &PendingToolCall{
		ToolCallID:   callID,
		ToolName:     "test_hil_tool",
		Kind:         PendingCallHILApproval,
		Index:        0,
		HILRequestID: hilReqID,
		Timeout:      5 * time.Minute,
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall(callID, "test_hil_tool", `{}`)
	agent.pausedTasks.Store(state)
	return state
}

func alwaysOKTool() stubExecFn {
	return func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.OK("ok"), nil
	}
}

func authResp(taskID, callID, credKey string) *a2a.AuthResponseData {
	return &a2a.AuthResponseData{
		Type:              "auth_response",
		TargetAgentTaskID: taskID,
		FunctionCallID:    callID,
		CredentialKey:     credKey,
		Code:              "test-auth-code",
	}
}

func assistantMsgWithOneToolCall(callID, name, argsJSON string) llm.ChatMessage {
	return llm.ChatMessage{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{ID: callID, Name: name, Arguments: argsJSON},
		},
	}
}

func pausedStateWithAuthEntry(t *testing.T, agent *Agent, taskID, callID string) *TaskExecutionState {
	t.Helper()
	state := newTestTaskState(agent)
	state.TaskID = taskID
	state.UserProps.UserID = "u1"
	state.TotalPending = 1
	state.PendingCalls[callID] = &PendingToolCall{
		ToolCallID:   callID,
		ToolName:     "stubtool",
		Kind:         PendingCallAuth,
		Index:        0,
		OriginalArgs: map[string]any{"q": "test"},
	}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall(callID, "stubtool", `{"q":"test"}`)
	agent.pausedTasks.Store(state)
	return state
}
