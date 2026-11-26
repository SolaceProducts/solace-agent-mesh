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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func delegatedTaskResponse(t *testing.T, state a2a.TaskState, text string) *broker.Message {
	t.Helper()
	return delegatedTaskResponseWithParts(t, state, map[string]any{"kind": "text", "text": text})
}

func delegatedTaskResponseWithStructuredResult(t *testing.T, state a2a.TaskState, result map[string]any) *broker.Message {
	t.Helper()
	result["type"] = "structured_invocation_result"
	return delegatedTaskResponseWithParts(t, state, map[string]any{"kind": "data", "data": result})
}

func delegatedTaskResponseWithParts(t *testing.T, state a2a.TaskState, parts ...any) *broker.Message {
	t.Helper()
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      "task-1",
		"result": map[string]any{
			"kind": "task",
			"id":   "task-1",
			"status": map[string]any{
				"state":   string(state),
				"message": map[string]any{"parts": parts},
			},
		},
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &broker.Message{Payload: payload}
}

func requireDelegatedFailure(t *testing.T, res *tool.Result) {
	t.Helper()
	if res.Status != tool.StatusError {
		t.Fatalf("status = %q, want %q: an unsuccessful delegated task must not reach the caller as success", res.Status, tool.StatusError)
	}
	published, _ := tool.PublishedResultData(res).(map[string]any)
	if published["error_code"] != tool.ErrorCodeDelegatedTaskFailed {
		t.Fatalf("published error_code = %v, want %q: %+v", published["error_code"], tool.ErrorCodeDelegatedTaskFailed, published)
	}
}

func TestParsePeerResponse_UnsuccessfulTaskBecomesToolError(t *testing.T) {
	for _, state := range []a2a.TaskState{a2a.TaskStateFailed, a2a.TaskStateCancelled, a2a.TaskStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			res, err := parsePeerResponse("hr-bot", delegatedTaskResponse(t, state, "everything broke"))
			if err != nil {
				t.Fatalf("parsePeerResponse: %v", err)
			}
			requireDelegatedFailure(t, res)
			if !strings.Contains(res.Message, "everything broke") {
				t.Errorf("message = %q, want the peer's own explanation", res.Message)
			}
			if res.Data == nil {
				t.Error("an unsuccessful peer result must still carry the task object as Data")
			}
		})
	}
}

func TestParsePeerResponse_CompletedTaskStaysSuccess(t *testing.T) {
	res, err := parsePeerResponse("hr-bot", delegatedTaskResponse(t, a2a.TaskStateCompleted, "all good"))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("a completed peer task must stay successful; got status %q", res.Status)
	}
}

func TestParsePeerResponse_StructuredWorkflowFailureKeepsItsReason(t *testing.T) {
	res, err := parsePeerResponse("invoice-flow",
		delegatedTaskResponseWithStructuredResult(t, a2a.TaskStateFailed, map[string]any{
			"status": "error", "error_message": `node "fetch" failed: tool execution failed (TOOL_EXIT_NONZERO)`,
		}))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	requireDelegatedFailure(t, res)
	if !strings.Contains(res.Message, "TOOL_EXIT_NONZERO") {
		t.Errorf("message = %q, want the workflow's failure reason", res.Message)
	}
	if strings.Contains(res.Message, "no text content") {
		t.Errorf("message = %q, must not claim there was no content", res.Message)
	}
}

func TestParsePeerResponse_FailedPeerWithoutTextSaysSo(t *testing.T) {
	res, err := parsePeerResponse("hr-bot", delegatedTaskResponseWithParts(t, a2a.TaskStateFailed))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	requireDelegatedFailure(t, res)
	if strings.Contains(res.Message, "responded") {
		t.Errorf("message = %q, a failed peer must not read as having responded", res.Message)
	}
}

func TestSubTaskParseResponse_FailedSubTaskBecomesToolError(t *testing.T) {
	res, err := (&subTaskTool{}).parseResponse(delegatedTaskResponse(t, a2a.TaskStateFailed, "the sub-task's tool crashed"))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	requireDelegatedFailure(t, res)
}

func TestSubTaskParseResponse_CompletedSubTaskStaysSuccess(t *testing.T) {
	res, err := (&subTaskTool{}).parseResponse(delegatedTaskResponse(t, a2a.TaskStateCompleted, "done"))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("a completed sub-task must stay successful; got status %q", res.Status)
	}
}

func TestWorkflowPeerParseResponse_FailedWorkflowBecomesToolError(t *testing.T) {
	wt := &workflowAgentTool{peerName: "invoice-flow"}
	res, err := wt.parseResponse(t.Context(),
		delegatedTaskResponse(t, a2a.TaskStateFailed, `Workflow "invoice-flow" failed: node "fetch" failed`), nil)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	requireDelegatedFailure(t, res)
}

func TestWorkflowPeerParseResponse_FailedWorkflowWithoutContentNeverSaysCompleted(t *testing.T) {
	wt := &workflowAgentTool{peerName: "invoice-flow"}
	res, err := wt.parseResponse(t.Context(), delegatedTaskResponseWithParts(t, a2a.TaskStateFailed), nil)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	requireDelegatedFailure(t, res)
	if strings.Contains(strings.ToLower(res.Message), "completed") {
		t.Errorf("message = %q: an error result must not tell the LLM the workflow completed", res.Message)
	}
}

func TestWorkflowPeerParseResponse_CompletedWorkflowStaysSuccess(t *testing.T) {
	wt := &workflowAgentTool{peerName: "invoice-flow"}
	res, err := wt.parseResponse(t.Context(),
		delegatedTaskResponse(t, a2a.TaskStateCompleted, "done"), nil)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("a completed workflow must stay successful; got status %q", res.Status)
	}
}

func TestWorkflowPeerParseResponse_CancelledWithStructuredResultIsToolError(t *testing.T) {
	wt := &workflowAgentTool{peerName: "invoice-flow"}
	res, err := wt.parseResponse(t.Context(),
		delegatedTaskResponseWithStructuredResult(t, a2a.TaskStateCancelled, map[string]any{"status": "cancelled"}), &tool.Context{})
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	requireDelegatedFailure(t, res)
}

func TestWorkflowPeerParseResponse_CompletedStructuredResultStaysSuccess(t *testing.T) {
	wt := &workflowAgentTool{peerName: "invoice-flow"}
	res, err := wt.parseResponse(t.Context(),
		delegatedTaskResponseWithStructuredResult(t, a2a.TaskStateCompleted, map[string]any{"status": "success"}), &tool.Context{})
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("a completed structured workflow result must stay successful; got status %q", res.Status)
	}
	if res.Data["partial_failure_message"] != nil {
		t.Errorf("a clean run must not report a partial failure: %+v", res.Data)
	}
}

func TestWorkflowPeerParseResponse_PartialFailureReachesTheCaller(t *testing.T) {
	const lost = `node "fetch" failed and was skipped: tool execution failed`
	wt := &workflowAgentTool{peerName: "invoice-flow"}
	res, err := wt.parseResponse(t.Context(),
		delegatedTaskResponseWithStructuredResult(t, a2a.TaskStateCompleted, map[string]any{
			"status": "success", "error_message": lost,
		}), &tool.Context{})
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("a best-effort run stays successful; got status %q", res.Status)
	}
	if !strings.Contains(res.Message, lost) {
		t.Errorf("message = %q, want it to name the lost node", res.Message)
	}
	if res.Data["partial_failure_message"] != lost {
		t.Errorf("partial_failure_message = %v, want %q", res.Data["partial_failure_message"], lost)
	}
}

var crashToolTurn = []llm.StreamChunk{
	{ContentDelta: "Running the report tool."},
	{ToolCallDeltas: []llm.ToolCallDelta{{Index: 0, ID: "call-crash", Name: "crash_tool", ArgumentsDelta: "{}"}}},
	{FinishReason: "tool_calls"},
}

func registerCrashTool(t *testing.T) func(*Agent) {
	return func(a *Agent) {
		if err := a.Tools().Register(&testBuiltinTool{
			name:        "crash_tool",
			description: "A tool whose process exits non-zero",
			params:      &tool.JSONSchema{Type: "object"},
			execFn: stubFailedTool(&tool.ToolFailedDetail{
				Code:    "TOOL_EXIT_NONZERO",
				Message: "process exited with code 1\n",
			}),
		}); err != nil {
			t.Fatalf("register crash_tool: %v", err)
		}
	}
}

func TestStructuredInvocation_ToolCrashFailsTheNode(t *testing.T) {
	cases := []struct {
		name  string
		final []llm.StreamChunk
	}{
		{name: "valid output after the crash", final: outputWithResultEmbed},
		{name: "invalid output after the crash spends no retry", final: outputWithoutResultEmbed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "task-si-crash-" + strings.ReplaceAll(tc.name, " ", "-")
			llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{crashToolTurn, tc.final}}

			resp, _ := runStructuredInvocationWithBudget(t.Context(), t, taskID, llmMock, 2, registerCrashTool(t))

			if got := llmMock.streamCalls(); got != 2 {
				t.Errorf("LLM stream calls = %d, want 2: the crash decides the outcome, so no validation retry is sent", got)
			}
			state, data := structuredResultData(t, resp)
			if state != a2a.TaskStateFailed || data["status"] != "error" {
				t.Fatalf("task state = %q, result status = %v, want failed/error: %+v", state, data["status"], data)
			}
			msg, _ := data["error_message"].(string)
			if !strings.Contains(msg, "TOOL_EXIT_NONZERO") {
				t.Errorf("error_message = %q, want the tool failure code", msg)
			}
			if msg != strings.TrimSpace(msg) {
				t.Errorf("error_message = %q: a tool's trailing stderr newline must not reach the workflow's reply", msg)
			}
		})
	}
}

func TestToolCrashInPausedBatchStillFailsTheTask(t *testing.T) {
	const ns, name, taskID = "ns", "paused-crash-agent", "task-paused-crash"

	mock := newMockLLM(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{
				{ID: "call-crash", Name: "crash_tool", Arguments: "{}"},
				{ID: "call-wf", Name: "workflow_self", Arguments: "{}"},
			},
		},
		&llm.ChatResponse{Content: "The report tool crashed.", FinishReason: "stop"},
	)
	agent := New(AgentConfig{Name: name, Namespace: ns, MaxLLMCalls: 5})
	svc := newMockServices(mock, newMemSessionStore())
	brk := newMockBroker()
	svc.brk = brk
	svc.arts = &artifact.ScopedService{Inner: memoryArtifactStore(), Scope: "namespace", Namespace: ns}
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.logger = discardLogger()
	agent.runCtx = t.Context()
	registerCrashTool(t)(agent)

	deliverCh := make(chan func(*tool.Result), 1)
	if err := agent.Tools().Register(&testBuiltinTool{
		name:        "workflow_self",
		description: "pausing delegation tool",
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for workflow", tool.WithData(map[string]any{
				"sub_task_id":  "st",
				"pending_kind": "workflow",
			}))
			r.OnDelivery = func(deliver func(*tool.Result)) { deliverCh <- deliver }
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}

	submitTaskMessage(t, agent, ns, name, taskID, "ctx-paused-crash")
	waitForPausedTask(t, agent, taskID)

	select {
	case deliver := <-deliverCh:
		deliver(tool.OK("workflow finished"))
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher never invoked the tool's OnDelivery")
	}

	if got := waitForReplyTaskState(t, brk, "reply/"+taskID); got != a2a.TaskStateFailed {
		t.Fatalf("final task state = %q, want %q", got, a2a.TaskStateFailed)
	}
}

func waitForReplyTaskState(t *testing.T, brk *mockBroker, topic string) a2a.TaskState {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		for _, m := range brk.getPublished() {
			if m.topic != topic {
				continue
			}
			var resp struct {
				Result a2a.Task `json:"result"`
			}
			if err := json.Unmarshal(m.payload, &resp); err != nil {
				t.Fatalf("decode reply: %v", err)
			}
			return resp.Result.Status.State
		}
		select {
		case <-deadline:
			t.Fatalf("no reply published on %q", topic)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
