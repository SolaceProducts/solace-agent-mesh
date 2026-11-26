// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	successTopic = "result/success"
	errorTopic   = "result/error"
)

func successAndErrorOutputs() map[string]*OutputHandlerConfig {
	return map[string]*OutputHandlerConfig{
		"ok_out": {
			Name:              "ok_out",
			TopicExpression:   "static:" + successTopic,
			PayloadExpression: "task_response:data",
			PayloadFormat:     "json",
		},
		"err_out": {
			Name:              "err_out",
			TopicExpression:   "static:" + errorTopic,
			PayloadExpression: "task_response:",
			PayloadFormat:     "json",
		},
	}
}

func workflowRule() *EventHandlerConfig {
	return &EventHandlerConfig{TargetWorkflowName: "ClassifyWorkflow", OnSuccess: "ok_out", OnError: "err_out"}
}

func agentRule() *EventHandlerConfig {
	return &EventHandlerConfig{TargetAgentName: "classifier", OnSuccess: "ok_out", OnError: "err_out"}
}

func finalTask(state a2a.TaskState, text string) *a2a.Task {
	return &a2a.Task{
		ID:   "t",
		Kind: "task",
		Status: a2a.TaskStatus{
			State: state,
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart(text)},
			},
		},
		Metadata: map[string]any{"token_usage": map[string]any{"input_tokens": 12}},
	}
}

func publishedOn(msgs []mockPublished, topic string) []mockPublished {
	var out []mockPublished
	for _, m := range msgs {
		if m.Topic == topic {
			out = append(out, m)
		}
	}
	return out
}

func errorOnlyPayload(message string) map[string]any {
	return map[string]any{
		"text":              message,
		"files":             nil,
		"data":              nil,
		"structured_result": nil,
		"structured_output": nil,
		"a2a_task_response": map[string]any{
			"error": map[string]any{"code": float64(taskFailedErrorCode), "message": message},
		},
	}
}

func soleErrorPayload(t *testing.T, mockBrk *mockPublishBroker) map[string]any {
	t.Helper()
	msgs := mockBrk.published()
	if got := publishedOn(msgs, successTopic); len(got) != 0 {
		t.Fatalf("published %d messages on the success topic, want 0: %s", len(got), got[0].Payload)
	}
	errMsgs := publishedOn(msgs, errorTopic)
	if len(errMsgs) != 1 {
		t.Fatalf("published %d messages on the error topic, want 1 (all: %+v)", len(errMsgs), msgs)
	}
	var payload map[string]any
	if err := json.Unmarshal(errMsgs[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	return payload
}

func TestHandleFinalResponse_FailedWorkflowRun_PublishesErrorOnlyOnErrorHandler(t *testing.T) {
	for _, state := range []a2a.TaskState{a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, successAndErrorOutputs())
			a.dataBroker = mockBrk

			var acked, nacked atomic.Bool
			a.holdFor(t, "task-failed", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack", NackOutcome: "rejected"})
			a.seedPending(t, "task-failed", &pendingTask{handlerCfg: workflowRule(), deferredACK: true})

			const failure = "Workflow node 'classify' failed: upstream timeout"
			a.handleFinalResponse(t.Context(), "task-failed", finalTask(state, failure))

			payload := soleErrorPayload(t, mockBrk)
			if want := errorOnlyPayload(failure); !reflect.DeepEqual(payload, want) {
				t.Errorf("error payload = %v, want exactly %v (no task status or metadata)", payload, want)
			}

			if a.heldCount(t) != 0 {
				t.Errorf("pending deferred ACKs = %d, want 0", a.heldCount(t))
			}
			if !acked.Load() || nacked.Load() {
				t.Errorf("deferred ACK acked=%v nacked=%v, want settled once the failure is published", acked.Load(), nacked.Load())
			}
		})
	}
}

func TestHandleFinalResponse_FailedWorkflowRun_ExpressionTarget_UsesErrorHandler(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, successAndErrorOutputs())
	a.dataBroker = mockBrk
	a.seedPending(t, "task-failed", &pendingTask{
		handlerCfg: &EventHandlerConfig{TargetWorkflowNameExpr: "input.payload:workflow", OnSuccess: "ok_out", OnError: "err_out"},
	})

	a.handleFinalResponse(t.Context(), "task-failed", finalTask(a2a.TaskStateFailed, "boom"))

	if payload := soleErrorPayload(t, mockBrk); !reflect.DeepEqual(payload, errorOnlyPayload("boom")) {
		t.Errorf("error payload = %v, want %v", payload, errorOnlyPayload("boom"))
	}
}

func TestHandleFinalResponse_FailedAgentRun_WithOnError_KeepsSuccessHandler(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, successAndErrorOutputs())
	a.dataBroker = mockBrk

	var acked, nacked atomic.Bool
	a.holdFor(t, "task-failed", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
	a.seedPending(t, "task-failed", &pendingTask{handlerCfg: agentRule(), deferredACK: true})

	task := finalTask(a2a.TaskStateFailed, "recovered answer")
	task.Status.Message.Parts = append(task.Status.Message.Parts, a2a.NewDataPart(map[string]any{"verdict": "approve"}))
	a.handleFinalResponse(t.Context(), "task-failed", task)

	msgs := mockBrk.published()
	if len(msgs) != 1 || msgs[0].Topic != successTopic {
		t.Fatalf("published %+v, want one message on %s", msgs, successTopic)
	}
	if got := string(msgs[0].Payload); got != `[{"verdict":"approve"}]` {
		t.Errorf("payload = %s, want the data parts", got)
	}
	if !acked.Load() || nacked.Load() {
		t.Errorf("deferred ACK acked=%v nacked=%v, want the unchanged success settle", acked.Load(), nacked.Load())
	}
}

func TestFailureErrorShape_SameForBothErrorPaths(t *testing.T) {
	for _, expr := range []string{"task_response:a2a_task_response.error.message", "task_response:text"} {
		t.Run(expr, func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
				"err_out": {
					Name:              "err_out",
					TopicExpression:   "static:" + errorTopic,
					PayloadExpression: expr,
					PayloadFormat:     "text",
				},
			})
			a.dataBroker = mockBrk
			rule := &EventHandlerConfig{TargetWorkflowName: "ClassifyWorkflow", OnError: "err_out"}
			a.seedPending(t, "rpc-error", &pendingTask{handlerCfg: rule})
			a.seedPending(t, "task-failed", &pendingTask{handlerCfg: rule})

			a.handleErrorResponse(t.Context(), "rpc-error", &a2a.Error{Code: -32603, Message: "rpc failure"})
			a.handleFinalResponse(t.Context(), "task-failed", finalTask(a2a.TaskStateFailed, "task failure"))

			msgs := mockBrk.published()
			if len(msgs) != 2 {
				t.Fatalf("published %d messages, want 2", len(msgs))
			}
			if got := string(msgs[0].Payload); got != "rpc failure" {
				t.Errorf("JSON-RPC error payload = %q, want %q", got, "rpc failure")
			}
			if got := string(msgs[1].Payload); got != "task failure" {
				t.Errorf("failed-task payload = %q, want %q", got, "task failure")
			}
		})
	}
}

func TestHandleFinalResponse_FailedWorkflowRun_NoOnError_KeepsSuccessHandler(t *testing.T) {
	cases := map[string]*EventHandlerConfig{
		"on_error unset":            {TargetWorkflowName: "ClassifyWorkflow", OnSuccess: "text_out"},
		"on_error names no handler": {TargetWorkflowName: "ClassifyWorkflow", OnSuccess: "text_out", OnError: "missing"},
	}
	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, map[string]*OutputHandlerConfig{
				"text_out": {
					Name:              "text_out",
					TopicExpression:   "static:" + successTopic,
					PayloadExpression: "task_response:text",
					PayloadFormat:     "text",
				},
			})
			a.dataBroker = mockBrk

			var acked, nacked atomic.Bool
			a.holdFor(t, "task-failed", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
			a.seedPending(t, "task-failed", &pendingTask{handlerCfg: rule, deferredACK: true})

			a.handleFinalResponse(t.Context(), "task-failed", finalTask(a2a.TaskStateFailed, "boom"))

			msgs := mockBrk.published()
			if len(msgs) != 1 || msgs[0].Topic != successTopic {
				t.Fatalf("published %+v, want one message on %s", msgs, successTopic)
			}
			if got := string(msgs[0].Payload); got != "boom" {
				t.Errorf("payload = %q, want %q", got, "boom")
			}
			if !acked.Load() || nacked.Load() {
				t.Errorf("deferred ACK acked=%v nacked=%v, want the unchanged success settle", acked.Load(), nacked.Load())
			}
		})
	}
}

func TestHandleFinalResponse_CompletedWorkflowRun_WithOnError_UsesSuccessHandler(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, successAndErrorOutputs())
	a.dataBroker = mockBrk

	var acked, nacked atomic.Bool
	a.holdFor(t, "task-ok", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
	a.seedPending(t, "task-ok", &pendingTask{handlerCfg: workflowRule(), deferredACK: true})

	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{{Kind: "data", Data: map[string]any{"verdict": "approve"}}},
			},
		},
	}
	a.handleFinalResponse(t.Context(), "task-ok", task)

	msgs := mockBrk.published()
	if len(msgs) != 1 || msgs[0].Topic != successTopic {
		t.Fatalf("published %+v, want one message on %s", msgs, successTopic)
	}
	if got := string(msgs[0].Payload); got != `[{"verdict":"approve"}]` {
		t.Errorf("payload = %s, want the data parts", got)
	}
	if !acked.Load() || nacked.Load() {
		t.Errorf("deferred ACK acked=%v nacked=%v, want settled as success", acked.Load(), nacked.Load())
	}
}

func TestHandleFinalResponse_FailedWorkflowRun_MessageSources(t *testing.T) {
	cases := []struct {
		name string
		task *a2a.Task
		want string
	}{
		{
			name: "structured invocation error_message",
			task: &a2a.Task{
				Status: a2a.TaskStatus{
					State: a2a.TaskStateFailed,
					Message: &a2a.Message{
						Role: "agent",
						Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
							"type":          string(a2a.SignalStructuredInvocationRes),
							"status":        "error",
							"error_message": "output failed schema validation",
							"retry_count":   2,
						})},
					},
				},
			},
			want: "output failed schema validation",
		},
		{
			name: "metadata error",
			task: &a2a.Task{
				Status:   a2a.TaskStatus{State: a2a.TaskStateFailed},
				Metadata: map[string]any{"error": `node "call" timed out`},
			},
			want: `node "call" timed out`,
		},
		{
			name: "state fallback",
			task: &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}},
			want: `task ended in state "canceled"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockBrk := &mockPublishBroker{}
			a := newTestAdapter(nil, successAndErrorOutputs())
			a.dataBroker = mockBrk
			a.seedPending(t, "task-failed", &pendingTask{handlerCfg: workflowRule()})

			a.handleFinalResponse(t.Context(), "task-failed", tc.task)

			if payload := soleErrorPayload(t, mockBrk); !reflect.DeepEqual(payload, errorOnlyPayload(tc.want)) {
				t.Errorf("error payload = %v, want %v", payload, errorOnlyPayload(tc.want))
			}
		})
	}
}

func unresolvableErrorOutputs() map[string]*OutputHandlerConfig {
	outputs := successAndErrorOutputs()
	outputs["err_out"].TopicExpression = "input.payload:nonexistent"
	return outputs
}

func captureLogs(a *EventMeshAdapter) *bytes.Buffer {
	var buf bytes.Buffer
	a.logger = slog.New(slog.NewJSONHandler(&buf, nil))
	return &buf
}

func assertLoggedPublishFailure(t *testing.T, logs string, wantMsg string) {
	t.Helper()
	for line := range strings.Lines(logs) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if rec["msg"] == wantMsg && rec["level"] == "ERROR" && rec["handlerName"] == "err_out" {
			return
		}
	}
	t.Errorf("no ERROR %q log with handlerName=err_out; logs:\n%s", wantMsg, logs)
}

func TestHandleFinalResponse_FailedWorkflowRun_ErrorPublishFails_RejectsDeferredACK(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, unresolvableErrorOutputs())
	a.dataBroker = mockBrk
	logs := captureLogs(a)

	var acked, nacked atomic.Bool
	a.holdFor(t, "task-failed", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
	a.seedPending(t, "task-failed", &pendingTask{handlerCfg: workflowRule(), deferredACK: true})

	a.handleFinalResponse(t.Context(), "task-failed", finalTask(a2a.TaskStateFailed, "boom"))

	if msgs := mockBrk.published(); len(msgs) != 0 {
		t.Fatalf("published %+v, want nothing when the on_error topic resolves empty", msgs)
	}
	if acked.Load() || !nacked.Load() {
		t.Errorf("deferred ACK acked=%v nacked=%v, want nacked through on_failure", acked.Load(), nacked.Load())
	}
	assertLoggedPublishFailure(t, logs.String(), "failed to publish error response")
}

func TestHandleErrorResponse_ErrorPublishFails_LogsAndRejectsDeferredACK(t *testing.T) {
	mockBrk := &mockPublishBroker{}
	a := newTestAdapter(nil, unresolvableErrorOutputs())
	a.dataBroker = mockBrk
	logs := captureLogs(a)

	var acked, nacked atomic.Bool
	a.holdFor(t, "rpc-error", testMsg(&acked, &nacked), AckFailurePolicy{Action: "nack"})
	a.seedPending(t, "rpc-error", &pendingTask{handlerCfg: agentRule(), deferredACK: true})

	a.handleErrorResponse(t.Context(), "rpc-error", &a2a.Error{Code: -32603, Message: "rpc failure"})

	if msgs := mockBrk.published(); len(msgs) != 0 {
		t.Fatalf("published %+v, want nothing when the on_error topic resolves empty", msgs)
	}
	if acked.Load() || !nacked.Load() {
		t.Errorf("deferred ACK acked=%v nacked=%v, want nacked through on_failure", acked.Load(), nacked.Load())
	}
	assertLoggedPublishFailure(t, logs.String(), "failed to publish error response")
}
