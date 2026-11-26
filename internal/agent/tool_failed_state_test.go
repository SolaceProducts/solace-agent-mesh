// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func stubFailedTool(detail *tool.ToolFailedDetail) stubExecFn {
	return func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
		return tool.Failed("tool crashed", detail), nil
	}
}

func TestDispatchToolCallsMayPause_StatusFailed_FlowsToLLM(t *testing.T) {
	detail := &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "exited with code 2",
		StderrTail: "some stderr",
		StackTrace: "panic: oops",
	}
	agent := newTestAgentWithStubTool(t, "crash_tool", stubFailedTool(detail))
	state := newTestTaskState(agent)
	args := defaultPauseCallArgs(agent, state)

	toolCalls := singleCall("crash_tool", "call-f1", `{}`)
	results, paused := agent.dispatchToolCallsMayPause(t.Context(), state, toolCalls, args.toolCtx, args.sp, args.parallelGroupID, args.sanitizeErrors, agent.logger)

	if paused {
		t.Fatal("should not be paused on StatusFailed")
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != tool.StatusFailed {
		t.Errorf("result status = %q, want %q", results[0].Status, tool.StatusFailed)
	}
	if results[0].FailedDetail == nil {
		t.Fatal("FailedDetail is nil")
	}
	if results[0].FailedDetail.StackTrace != "panic: oops" {
		t.Errorf("StackTrace = %q", results[0].FailedDetail.StackTrace)
	}
}

func TestToolTimeout_TellsLLMNotToRetry(t *testing.T) {
	detail := &tool.ToolFailedDetail{
		Code:     "TOOL_TIMEOUT",
		Message:  `tool "sam-test-harness__generate_test_plan" timed out after 300s`,
		Guidance: "Do NOT retry this call and do NOT tell the user to retry.",
	}

	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-timeout1", Name: "slow_tool", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{
			Content:      "The tool timed out and needs an operator to raise its budget.",
			FinishReason: "stop",
		},
	)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "slow_tool",
		description: "A tool that times out",
		params:      &tool.JSONSchema{Type: "object"},
		execFn:      stubFailedTool(detail),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := agent.HandleTask(
		t.Context(),
		"task-timeout-001",
		newUserMessage("Generate a test plan"),
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	); err != nil {
		t.Fatalf("HandleTask error: %v", err)
	}

	llmCalls := mock.getCalls()
	if len(llmCalls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(llmCalls))
	}

	var foundToolMsg bool
	for _, m := range llmCalls[1].Messages {
		if m.Role != "tool" {
			continue
		}
		foundToolMsg = true
		var payload map[string]any
		if err := json.Unmarshal([]byte(m.Content), &payload); err != nil {
			t.Fatalf("tool message is not valid JSON: %v — content: %s", err, m.Content)
		}
		if payload["code"] != "TOOL_TIMEOUT" {
			t.Errorf("code = %v, want TOOL_TIMEOUT", payload["code"])
		}
		if payload["retryable"] != false {
			t.Errorf("retryable = %v, want false; the model must be told a timeout is not transient", payload["retryable"])
		}
		guidance, _ := payload["guidance"].(string)
		if !strings.Contains(guidance, "Do NOT retry") {
			t.Errorf("guidance = %q, want an explicit non-retry directive", guidance)
		}
	}
	if !foundToolMsg {
		t.Error("second LLM call contained no tool message")
	}
}

func TestStatusFailed_FlowsToLLMAndEmitsFailedState(t *testing.T) {
	detail := &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "process exited with code 1",
		StderrTail: "Traceback (most recent call last): ...",
		StackTrace: "RuntimeError: boom",
	}

	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-fail1", Name: "crash_tool", Arguments: `{}`},
			},
		},
		&llm.ChatResponse{
			Content:      "The tool failed because the process exited unexpectedly.",
			FinishReason: "stop",
		},
	)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "crash_tool",
		description: "A tool that crashes",
		params:      &tool.JSONSchema{Type: "object"},
		execFn:      stubFailedTool(detail),
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := agent.HandleTask(
		t.Context(),
		"task-failed-001",
		newUserMessage("Run the tool"),
		a2a.UserProperties{ClientID: "u1", UserID: "u1"},
	)
	if err != nil {
		t.Fatalf("HandleTask error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected A2A error: %+v", resp.Error)
	}

	llmCalls := mock.getCalls()
	if len(llmCalls) != 2 {
		t.Fatalf("expected 2 LLM calls (tool call + explanation), got %d", len(llmCalls))
	}

	secondCallMsgs := llmCalls[1].Messages
	var foundToolMsg bool
	for _, m := range secondCallMsgs {
		if m.Role == "tool" {
			foundToolMsg = true
			var payload map[string]any
			if unmarshalErr := json.Unmarshal([]byte(m.Content), &payload); unmarshalErr != nil {
				t.Fatalf("tool message content is not valid JSON: %v — content: %s", unmarshalErr, m.Content)
			}
			if payload["status"] != tool.StatusFailed {
				t.Errorf("status = %v, want %q", payload["status"], tool.StatusFailed)
			}
			if payload["code"] != "TOOL_EXIT_NONZERO" {
				t.Errorf("code = %v, want TOOL_EXIT_NONZERO", payload["code"])
			}
			stderrVal, _ := payload["stderr_tail"].(string)
			if !strings.Contains(stderrVal, "Traceback") {
				t.Errorf("stderr_tail = %v, missing expected content", stderrVal)
			}
			stackVal, _ := payload["stack_trace"].(string)
			if !strings.Contains(stackVal, "RuntimeError") {
				t.Errorf("stack_trace = %v, missing expected content", stackVal)
			}
		}
	}
	if !foundToolMsg {
		t.Error("second LLM call did not contain a tool message")
	}

	rawResp, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	var outer struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(rawResp, &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}
	var task a2a.Task
	if err := json.Unmarshal(outer.Result, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if task.Status.State != a2a.TaskStateFailed {
		t.Errorf("final state = %q, want %q", task.Status.State, a2a.TaskStateFailed)
	}
	if task.Status.Message == nil {
		t.Fatal("status message is nil")
	}
	var foundExplanation bool
	for _, p := range task.Status.Message.Parts {
		if p.Kind == "text" {
			text := strings.ToLower(p.Text)
			if strings.Contains(text, "tool failed") || strings.Contains(text, "process exited") || strings.Contains(text, "unexpectedly") {
				foundExplanation = true
			}
		}
	}
	if !foundExplanation {
		t.Logf("task parts: %+v", task.Status.Message.Parts)
		t.Error("expected LLM explanation in final message parts, not found")
	}
}

func TestBuildToolFailedResponse_TaskState(t *testing.T) {
	detail := &tool.ToolFailedDetail{
		Code:       "TOOL_EXIT_NONZERO",
		Message:    "exited",
		StderrTail: "stderr",
		StackTrace: "traceback",
	}
	resp := buildToolFailedResponse("task-001", "ctx-002", "my-agent", detail)

	if resp == nil {
		t.Fatal("response is nil")
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var outer struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}

	var task a2a.Task
	if err := json.Unmarshal(outer.Result, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}

	if task.Status.State != a2a.TaskStateFailed {
		t.Errorf("state = %q, want %q", task.Status.State, a2a.TaskStateFailed)
	}
	if task.ID != "task-001" {
		t.Errorf("id = %q", task.ID)
	}
	if task.Metadata["agent_name"] != "my-agent" {
		t.Errorf("agent_name = %v", task.Metadata["agent_name"])
	}

	if task.Status.Message == nil {
		t.Fatal("status message nil")
	}
	var foundSignal bool
	for _, p := range task.Status.Message.Parts {
		if p.Kind == "data" {
			d, _ := json.Marshal(p.Data)
			var m map[string]any
			_ = json.Unmarshal(d, &m)
			if m["a2a_signal_type"] == "tool_execution_error" {
				foundSignal = true
				if m["stderr_tail"] != "stderr" {
					t.Errorf("stderr_tail = %v", m["stderr_tail"])
				}
				if m["stack_trace"] != "traceback" {
					t.Errorf("stack_trace = %v", m["stack_trace"])
				}
			}
		}
	}
	if !foundSignal {
		t.Error("tool_execution_error signal not found in status message parts")
	}
}

func TestBuildToolFailedResponse_NilDetail(t *testing.T) {
	resp := buildToolFailedResponse("task-002", "ctx-003", "agent", nil)
	if resp == nil {
		t.Fatal("nil")
	}
	data, _ := json.Marshal(resp)
	var outer struct {
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(data, &outer)
	var task a2a.Task
	_ = json.Unmarshal(outer.Result, &task)
	if task.Status.State != a2a.TaskStateFailed {
		t.Errorf("state = %q", task.Status.State)
	}
}
