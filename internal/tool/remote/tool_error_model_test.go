// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func simulateSTRError(brk *broker.DevBroker, corrID, replyTo string, invokeErr remotetool.InvokeError) {
	errResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      corrID,
		"error":   invokeErr,
	}
	payload, _ := json.Marshal(errResp)
	_ = brk.Publish(context.Background(), replyTo, &broker.Message{
		Payload:       payload,
		CorrelationID: corrID,
	})
}

func TestExecutor_StructuredErrorFields_PreservedThrough(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "failing_tool", func(corrID, replyTo, _ string) {
		simulateSTRError(brk, corrID, replyTo, remotetool.InvokeError{
			Code:       remotetool.ErrToolExitNonzero,
			Message:    "tool exited with code 1: some error",
			StderrTail: "last lines of stderr",
			StackTrace: "Traceback (most recent call last):\n  File tool.py",
		})
	})

	_, err := exe.Invoke(t.Context(), "corr-struct-1", &InvokeRequest{
		ToolName:       "failing_tool",
		TimeoutSeconds: 5,
	}, nil)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	invokeErr, ok := err.(*InvokeError)
	if !ok {
		t.Fatalf("expected *InvokeError, got %T: %v", err, err)
	}
	if invokeErr.Code != remotetool.ErrToolExitNonzero {
		t.Errorf("code = %q, want %q", invokeErr.Code, remotetool.ErrToolExitNonzero)
	}
	if invokeErr.StderrTail != "last lines of stderr" {
		t.Errorf("StderrTail = %q, want %q", invokeErr.StderrTail, "last lines of stderr")
	}
	if invokeErr.StackTrace == "" {
		t.Error("StackTrace should be preserved")
	}
}

func TestTool_Execute_InvokeError_ReturnsStatusFailed(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "crash_tool", func(corrID, replyTo, _ string) {
		simulateSTRError(brk, corrID, replyTo, remotetool.InvokeError{
			Code:       remotetool.ErrToolExitNonzero,
			Message:    "tool exited with code 2",
			StderrTail: "stderr output here",
			StackTrace: "panic: something bad",
		})
	})

	toolCfg := &ToolConfig{
		ToolName:       "crash_tool",
		TimeoutSeconds: 5,
	}
	remoteTool := NewTool(toolCfg, exe)

	result, err := remoteTool.Execute(t.Context(), map[string]any{}, &tool.Context{
		AgentName: "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
	})

	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Status != tool.StatusFailed {
		t.Errorf("status = %q, want %q", result.Status, tool.StatusFailed)
	}
	if result.ErrorCode != tool.ErrorCodeToolFailed {
		t.Errorf("error_code = %q, want %q", result.ErrorCode, tool.ErrorCodeToolFailed)
	}
	if result.FailedDetail == nil {
		t.Fatal("FailedDetail is nil")
	}
	if result.FailedDetail.Code != string(remotetool.ErrToolExitNonzero) {
		t.Errorf("detail.Code = %q, want %q", result.FailedDetail.Code, remotetool.ErrToolExitNonzero)
	}
	if result.FailedDetail.StderrTail != "stderr output here" {
		t.Errorf("detail.StderrTail = %q", result.FailedDetail.StderrTail)
	}
	if result.FailedDetail.StackTrace != "panic: something bad" {
		t.Errorf("detail.StackTrace = %q", result.FailedDetail.StackTrace)
	}
}

func TestTool_Execute_RetryableError_ReturnsStatusFailed(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	for range 2 {
		simulateSTR(t, brk, "timeout_tool", func(corrID, replyTo, _ string) {
			simulateSTRError(brk, corrID, replyTo, remotetool.InvokeError{
				Code:    remotetool.ErrToolTimeout,
				Message: "tool timed out",
			})
		})
	}

	toolCfg := &ToolConfig{
		ToolName:       "timeout_tool",
		TimeoutSeconds: 5,
	}
	remoteTool := NewTool(toolCfg, exe)
	result, err := remoteTool.Execute(t.Context(), map[string]any{}, &tool.Context{
		AgentName: "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Status != tool.StatusFailed {
		t.Errorf("status = %q, want %q", result.Status, tool.StatusFailed)
	}
}

func TestBuildToolFailedResponse_Shape(t *testing.T) {
	detail := &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "exited with code 1",
		StderrTail: "last stderr line",
		StackTrace: "Traceback...",
	}
	resp := buildToolFailedResponse("task-123", "ctx-456", "my-agent", detail)
	if resp == nil {
		t.Fatal("response is nil")
	}

	payload, _ := json.Marshal(resp)
	var outer struct {
		Result *a2a.Task `json:"result"`
	}
	if err := json.Unmarshal(payload, &outer); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	task := outer.Result
	if task == nil {
		t.Fatal("result task is nil")
	}
	if task.Status.State != a2a.TaskStateFailed {
		t.Errorf("state = %q, want %q", task.Status.State, a2a.TaskStateFailed)
	}
	if task.ID != "task-123" {
		t.Errorf("id = %q, want task-123", task.ID)
	}

	if task.Status.Message == nil || len(task.Status.Message.Parts) == 0 {
		t.Fatal("status message has no parts")
	}
	var foundErrorSignal bool
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "data" {
			data, ok := part.Data.(map[string]any)
			if !ok {
				continue
			}
			if data["a2a_signal_type"] == "tool_execution_error" {
				foundErrorSignal = true
				if data["code"] != "TOOL_EXIT_NONZERO" {
					t.Errorf("data.code = %v, want TOOL_EXIT_NONZERO", data["code"])
				}
				if data["stderr_tail"] != "last stderr line" {
					t.Errorf("data.stderr_tail = %v", data["stderr_tail"])
				}
			}
		}
	}
	if !foundErrorSignal {
		t.Error("no tool_execution_error data part found in status message")
	}
}

func TestTool_Execute_NonToolStarError_CarriesStderrAndStackTrace(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "flaky_tool", func(corrID, replyTo, _ string) {
		simulateSTRError(brk, corrID, replyTo, remotetool.InvokeError{
			Code:       remotetool.ErrExecutionError,
			Message:    "internal execution failure",
			StderrTail: "Error: unexpected nil pointer",
			StackTrace: "goroutine 1 [running]:\nmain.run()",
		})
	})

	toolCfg := &ToolConfig{
		ToolName:       "flaky_tool",
		TimeoutSeconds: 5,
	}
	remoteTool := NewTool(toolCfg, exe)

	result, err := remoteTool.Execute(t.Context(), map[string]any{}, &tool.Context{
		AgentName: "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.Status != tool.StatusError {
		t.Errorf("status = %q, want %q (non-TOOL_* errors keep StatusError for LLM recovery)", result.Status, tool.StatusError)
	}
	if result.FailedDetail == nil {
		t.Fatal("FailedDetail is nil — stderr and stack trace would be lost")
	}
	if result.FailedDetail.StderrTail != "Error: unexpected nil pointer" {
		t.Errorf("StderrTail = %q", result.FailedDetail.StderrTail)
	}
	if result.FailedDetail.StackTrace != "goroutine 1 [running]:\nmain.run()" {
		t.Errorf("StackTrace = %q", result.FailedDetail.StackTrace)
	}
}

func buildToolFailedResponse(taskID, contextID, agentName string, detail *tool.ToolFailedDetail) *a2a.Response {
	var parts []a2a.Part
	if detail != nil {
		parts = append(parts, a2a.NewDataPart(map[string]any{
			"a2a_signal_type": "tool_execution_error",
			"code":            detail.Code,
			"message":         detail.Message,
			"stderr_tail":     detail.StderrTail,
			"stack_trace":     detail.StackTrace,
		}))
	}
	parts = append(parts, a2a.NewTextPart("Tool execution failed."))
	responseMsg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-" + taskID + "-tool-failed",
		Role:      a2a.RoleAgent,
		Parts:     parts,
	}
	task := a2a.Task{
		ID:        taskID,
		ContextID: contextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateFailed,
			Message: &responseMsg,
		},
		Metadata: map[string]any{
			"agent_name": agentName,
		},
	}
	return a2a.NewResponse(taskID, task)
}
