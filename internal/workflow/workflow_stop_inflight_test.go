// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const stopInflightReplyTo = "test/reply/stop-inflight"

func startedWorkflowInstance(t *testing.T, cfg *WorkflowConfig) (*WorkflowInstance, *capturingBroker) {
	t.Helper()
	brk := &capturingBroker{}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return w, brk
}

func finalTaskOn(t *testing.T, msgs []capturedMessage, topic string) *a2a.Task {
	t.Helper()
	var found []*a2a.Task
	for _, m := range msgs {
		if m.Topic != topic {
			continue
		}
		var resp struct {
			Result *a2a.Task `json:"result"`
		}
		if err := json.Unmarshal(m.Payload, &resp); err != nil || resp.Result == nil {
			continue
		}
		if resp.Result.Kind == "task" {
			found = append(found, resp.Result)
		}
	}
	if len(found) > 1 {
		t.Fatalf("caller received %d terminal responses on %q, want at most 1", len(found), topic)
	}
	if len(found) == 0 {
		return nil
	}
	return found[0]
}

func cancelRequestsTo(msgs []capturedMessage, topic string) int {
	n := 0
	for _, m := range msgs {
		if m.Topic == topic && strings.Contains(string(m.Payload), `"tasks/cancel"`) {
			n++
		}
	}
	return n
}

func messageSendsTo(msgs []capturedMessage, topic string) int {
	n := 0
	for _, m := range msgs {
		if m.Topic == topic && strings.Contains(string(m.Payload), `"message/send"`) {
			n++
		}
	}
	return n
}

func requestIDSentTo(t *testing.T, msgs []capturedMessage, topic string) string {
	t.Helper()
	for _, m := range msgs {
		if m.Topic != topic || !strings.Contains(string(m.Payload), `"message/send"`) {
			continue
		}
		var req a2a.Request
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("unmarshal agent request: %v", err)
		}
		return req.ID
	}
	t.Fatalf("no message/send published to %q", topic)
	return ""
}

func deliverSubTaskResult(t *testing.T, w *WorkflowInstance, subTaskID string, state a2a.TaskState) {
	t.Helper()
	task := a2a.Task{
		ID:   subTaskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   state,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("done")}},
		},
	}
	payload, err := json.Marshal(a2a.NewResponse(subTaskID, task))
	if err != nil {
		t.Fatalf("marshal sub-task response: %v", err)
	}
	w.handleAgentResponse(t.Context(), a2a.Request{ID: subTaskID}, &broker.Message{Payload: payload})
}

func TestStop_FailsInFlightMapExecution(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "stop-inflight-map",
		Namespace: "test-ns",
		Nodes: []*NodeConfig{
			{ID: "fanout", Type: "map", ItemsExpression: []any{"a", "b", "c"}, TemplateNode: "item", ConcurrencyLimit: 1},
			{ID: "item", Type: "agent", AgentName: "itemWorker"},
		},
	}
	w, brk := startedWorkflowInstance(t, cfg)

	taskID := "019e0000-0000-7000-8000-00000000440a"
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: stopInflightReplyTo})
	w.handleTaskRequest(context.Background(), *req, bmsg)

	workerTopic := a2a.AgentRequestTopic("test-ns", "itemWorker")
	if got := messageSendsTo(brk.snapshot(), workerTopic); got != 1 {
		t.Fatalf("map with concurrency 1 dispatched %d items before stop, want 1", got)
	}
	if finalTaskOn(t, brk.snapshot(), stopInflightReplyTo) != nil {
		t.Fatal("execution finalized before Stop; test setup is wrong")
	}

	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	msgs := brk.snapshot()
	final := finalTaskOn(t, msgs, stopInflightReplyTo)
	if final == nil {
		t.Fatal("no terminal response published to the caller after Stop; the execution was dropped silently")
	}
	if final.Status.State != a2a.TaskStateFailed {
		t.Errorf("terminal state = %q, want %q", final.Status.State, a2a.TaskStateFailed)
	}
	if final.ID != taskID {
		t.Errorf("terminal response task id = %q, want %q", final.ID, taskID)
	}
	if got, _ := final.Metadata["error"].(string); got != workflowInstanceStoppedMsg {
		t.Errorf("terminal error = %q, want %q", got, workflowInstanceStoppedMsg)
	}
	if got := cancelRequestsTo(msgs, workerTopic); got != 1 {
		t.Errorf("tasks/cancel sent to the in-flight sub-task %d times, want 1", got)
	}
	if got := messageSendsTo(msgs, workerTopic); got != 1 {
		t.Errorf("map kept dispatching after Stop: %d item requests, want 1", got)
	}

	w.executionsMu.Lock()
	remaining := len(w.executions)
	w.executionsMu.Unlock()
	if remaining != 0 {
		t.Errorf("%d executions still registered after Stop, want 0", remaining)
	}
}

func onExitWorkflowConfig(name string) *WorkflowConfig {
	return &WorkflowConfig{
		Name:      name,
		Namespace: "test-ns",
		Nodes: []*NodeConfig{
			{ID: "main", Type: "agent", AgentName: "mainAgent"},
			{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"},
		},
		OnExit: &ExitHandler{Always: "cleanup"},
	}
}

func TestStop_SkipsOnExitHandlers(t *testing.T) {
	w, brk := startedWorkflowInstance(t, onExitWorkflowConfig("stop-skips-on-exit"))

	req, bmsg := authzTaskMessage("019e0000-0000-7000-8000-00000000440b", a2a.UserProperties{UserID: "alice", ReplyTo: stopInflightReplyTo})
	w.handleTaskRequest(context.Background(), *req, bmsg)
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	msgs := brk.snapshot()
	if got := messageSendsTo(msgs, a2a.AgentRequestTopic("test-ns", "cleanupAgent")); got != 0 {
		t.Errorf("on_exit handler dispatched %d times during stop, want 0", got)
	}
	final := finalTaskOn(t, msgs, stopInflightReplyTo)
	if final == nil {
		t.Fatal("no terminal response published after Stop")
	}
	if final.Status.State != a2a.TaskStateFailed {
		t.Errorf("terminal state = %q, want %q", final.Status.State, a2a.TaskStateFailed)
	}
}

func TestStop_DuringExitChainKeepsReachedOutcome(t *testing.T) {
	w, brk := startedWorkflowInstance(t, onExitWorkflowConfig("stop-during-exit-chain"))

	req, bmsg := authzTaskMessage("019e0000-0000-7000-8000-00000000440c", a2a.UserProperties{UserID: "alice", ReplyTo: stopInflightReplyTo})
	w.handleTaskRequest(context.Background(), *req, bmsg)
	mainSubTask := requestIDSentTo(t, brk.snapshot(), a2a.AgentRequestTopic("test-ns", "mainAgent"))
	deliverSubTaskResult(t, w, mainSubTask, a2a.TaskStateCompleted)

	if got := messageSendsTo(brk.snapshot(), a2a.AgentRequestTopic("test-ns", "cleanupAgent")); got != 1 {
		t.Fatalf("on_exit handler dispatched %d times before stop, want 1 (test setup)", got)
	}
	if finalTaskOn(t, brk.snapshot(), stopInflightReplyTo) != nil {
		t.Fatal("execution finalized before Stop; test setup is wrong")
	}

	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	final := finalTaskOn(t, brk.snapshot(), stopInflightReplyTo)
	if final == nil {
		t.Fatal("no terminal response published after Stop")
	}
	if final.Status.State != a2a.TaskStateCompleted {
		t.Errorf("terminal state = %q, want %q: the outcome reached before the exit chain", final.Status.State, a2a.TaskStateCompleted)
	}
}

type settleRecorder struct{ acks, nacks int }

func (s *settleRecorder) message(t *testing.T, taskID string) (*a2a.Request, *broker.Message) {
	t.Helper()
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: stopInflightReplyTo})
	delivered := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() { s.acks++ }, func() { s.nacks++ })
	delivered.UserProperties = bmsg.UserProperties
	return req, delivered
}

func stoppedErrorResponseFor(msgs []capturedMessage, taskID string) bool {
	for _, m := range msgs {
		var resp a2a.Response
		if m.Topic != stopInflightReplyTo || json.Unmarshal(m.Payload, &resp) != nil || resp.Error == nil {
			continue
		}
		if resp.ID == taskID && strings.Contains(resp.Error.Message, workflowInstanceStoppedMsg) {
			return true
		}
	}
	return false
}

func TestStop_RequestArrivingDuringStopGetsTerminalError(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "stop-admission",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	}
	w, brk := startedWorkflowInstance(t, cfg)
	stepTopic := a2a.AgentRequestTopic("test-ns", "stepAgent")

	w.failInFlightExecutions()

	const rejectedTaskID = "019e0000-0000-7000-8000-00000000440d"
	var rejected settleRecorder
	req, msg := rejected.message(t, rejectedTaskID)
	w.handleTaskRequest(context.Background(), *req, msg)
	if rejected.acks != 1 || rejected.nacks != 0 {
		t.Errorf("request during stop: acks=%d nacks=%d, want it acked and answered", rejected.acks, rejected.nacks)
	}
	if !stoppedErrorResponseFor(brk.snapshot(), rejectedTaskID) {
		t.Error("request during stop got no terminal error on ReplyTo")
	}

	if got := messageSendsTo(brk.snapshot(), stepTopic); got != 0 {
		t.Errorf("request admitted during stop dispatched %d node requests, want 0", got)
	}
	w.executionsMu.Lock()
	registered := len(w.executions)
	w.executionsMu.Unlock()
	if registered != 0 {
		t.Errorf("%d executions registered during stop, want 0", registered)
	}

	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	var afterRestart settleRecorder
	req, msg = afterRestart.message(t, "019e0000-0000-7000-8000-00000000440f")
	w.handleTaskRequest(context.Background(), *req, msg)
	if got := messageSendsTo(brk.snapshot(), stepTopic); got != 1 {
		t.Errorf("restarted instance did not admit a request: %d node requests, want 1", got)
	}
	if afterRestart.acks != 0 || afterRestart.nacks != 0 {
		t.Errorf("request of a running run: acks=%d nacks=%d, want it held until the run ends", afterRestart.acks, afterRestart.nacks)
	}
}
