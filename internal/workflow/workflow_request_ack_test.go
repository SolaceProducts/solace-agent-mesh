// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const requestAckReplyTo = "test/reply/request-ack"

type ackCounter struct{ n atomic.Int32 }

func (c *ackCounter) acks() int { return int(c.n.Load()) }

func deliveredRequest(taskID string, acks *ackCounter, redelivered bool) (*a2a.Request, *broker.Message) {
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: requestAckReplyTo})
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() { acks.n.Add(1) }, func() {})
	msg.UserProperties = bmsg.UserProperties
	msg.Redelivered = redelivered
	return req, msg
}

func executionFor(w *WorkflowInstance, taskID string) *WorkflowExecution {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	return w.executions[taskID]
}

func subTaskOf(t *testing.T, exec *WorkflowExecution, nodeID string) string {
	t.Helper()
	id, ok := exec.State.SubTaskForNode(nodeID)
	if !ok {
		t.Fatalf("no sub-task dispatched for node %q", nodeID)
	}
	return id
}

func newHILWorkflow(t *testing.T, nodes ...*NodeConfig) (*WorkflowInstance, *approvalArgTool) {
	t.Helper()
	w, _ := newHILInstance(t)
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes}
	w.dag = dag
	rec := newApprovalArgTool()
	ts := tool.NewSet()
	if err = ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	w.tools = ts
	w.hilToolConfigs = gatedCallConfig()
	ts.SetHILConfigs(w.hilToolConfigs)
	w.timerManager = NewInProcessTimerManager(slog.Default(), w.fireTimerEvent)
	go w.timerManager.Start(w.ctx)
	return w, rec
}

func otherAgentNode() *NodeConfig {
	return &NodeConfig{ID: "other", Type: "agent", AgentName: "otherAgent"}
}

func onlyPausedRow(t *testing.T, w *WorkflowInstance, taskID string) wfstore.TaskPause {
	t.Helper()
	pauses, err := w.pausedRepo.PausesForTask(t.Context(), taskID)
	if err != nil || len(pauses) != 1 {
		t.Fatalf("paused rows of the task = %v (err %v), want exactly 1", pauses, err)
	}
	return pauses[0]
}

func TestRequest_HeldWhileRunningAndAckedAtEnd(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "ack-at-end",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	}
	w, brk := startedWorkflowInstance(t, cfg)
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	const taskID = "019e0000-0000-7000-8000-0000000a0001"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	if acks.acks() != 0 {
		t.Fatalf("request acknowledged %d times while its run is running, want 0", acks.acks())
	}

	deliverSubTaskResult(t, w, requestIDSentTo(t, brk.snapshot(), a2a.AgentRequestTopic("test-ns", "stepAgent")), a2a.TaskStateCompleted)

	if finalTaskOn(t, brk.snapshot(), requestAckReplyTo) == nil {
		t.Fatal("run did not publish its final response")
	}
	if acks.acks() != 1 {
		t.Errorf("request acknowledged %d times after the run ended, want 1", acks.acks())
	}
}

func TestRequest_AckedOnlyWhenTheWholeRunIsParked(t *testing.T) {
	w, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-park"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)

	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()

	if got := onlyPausedRow(t, w, taskID); got.Status != "paused" {
		t.Fatalf("gated step row status = %q, want paused", got.Status)
	}
	if acks.acks() != 0 {
		t.Fatalf("request acknowledged %d times while another branch runs beside the pause, want 0", acks.acks())
	}

	exec := executionFor(w, taskID)
	deliverSubTaskResult(t, w, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)

	if finalized(exec) {
		t.Fatal("run finished although its gated step is still waiting for approval")
	}
	if acks.acks() != 1 {
		t.Errorf("request acknowledged %d times once every open step waits on its paused row, want 1", acks.acks())
	}
}

func TestRequest_ResumedHereStaysHeldUntilTheRunEnds(t *testing.T) {
	w, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-resume-here"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	requestID := onlyPausedRow(t, w, taskID).RequestID

	w.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	w.wg.Wait()

	if !rec.executed() {
		t.Fatal("approved tool did not run")
	}
	if acks.acks() != 0 {
		t.Fatalf("request acknowledged %d times after this process won the resume while a branch still runs, want 0", acks.acks())
	}
	exec := executionFor(w, taskID)
	deliverSubTaskResult(t, w, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)
	if !finalized(exec) || acks.acks() != 1 {
		t.Errorf("after the run ended: finalized=%v acks=%d, want true and 1", finalized(exec), acks.acks())
	}
}

func TestRequest_AnswerPassedOnIsAppliedByTheAWERunningTheRun(t *testing.T) {
	w, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-answer-passed-on"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	requestID := onlyPausedRow(t, w, taskID).RequestID
	answer := []byte(`{"type":"user_input_response","request_id":"` + requestID + `","status":"answered","data":{"approved":true}}`)
	res, err := w.runStore().AcceptAnswer(t.Context(), w.cfg.Name, requestID, "resumed", string(answer))
	if err != nil || res.Outcome != wfstore.AnswerRecorded {
		t.Fatalf("another AWE taking the answer: outcome=%v err=%v, want it recorded on the pause", res.Outcome, err)
	}

	w.handleForwardedAnswer(t.Context(), &broker.Message{Topic: a2a.WorkflowForwardTopic("test-ns", "wf"), Payload: answer})
	w.wg.Wait()

	if !rec.executed() {
		t.Fatal("the AWE running the run did not apply the answer passed on to it")
	}
	if got := onlyPausedRow(t, w, taskID).Status; got != "resumed" {
		t.Errorf("pause status = %q, want resumed", got)
	}
	if acks.acks() != 0 {
		t.Errorf("request acknowledged %d times while another step still runs, want 0", acks.acks())
	}
}

func insertPausedRow(t *testing.T, w *WorkflowInstance, taskID, requestID string) {
	t.Helper()
	if err := w.pausedRepo.Insert(t.Context(), wfstore.PausedWorkflow{
		RequestID:     requestID,
		ExecutionID:   idgen.New(),
		TaskID:        taskID,
		WorkflowName:  "wf",
		NodeID:        "call_gated",
		RequesterAddr: "test-ns/notify/" + requestID,
		StateSnapshot: `{}`,
		ExecContext:   `{}`,
		Routing:       `{"node_id":"call_gated"}`,
	}); err != nil {
		t.Fatalf("insert paused row: %v", err)
	}
}

func TestRedelivery_RunResumedElsewhereIsDropped(t *testing.T) {
	w, _ := newHILWorkflow(t, otherAgentNode())
	const taskID = "task-redelivered-carried"
	insertPausedRow(t, w, taskID, "req-answered")
	insertPausedRow(t, w, taskID, "req-open")
	if _, claimed, err := w.pausedRepo.Claim(t.Context(), "req-answered", "resumed"); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)

	w.handleTaskRequest(t.Context(), *req, msg)

	if acks.acks() != 1 {
		t.Errorf("redelivered request of a run another process carries: acks=%d, want 1", acks.acks())
	}
	if executionFor(w, taskID) != nil {
		t.Error("a second run started beside the one another process carries")
	}
	statuses, err := w.pausedRepo.StatusesFor(t.Context(), []string{"req-open"})
	if err != nil || statuses["req-open"] != "paused" {
		t.Errorf("the carried run's open pause = %v (err %v), want it left paused", statuses, err)
	}
}

func TestRequest_ResumedByTheHolderKeepsTheRunHeldByItsRequest(t *testing.T) {
	w, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-resumed-by-holder"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	requestID := onlyPausedRow(t, w, taskID).RequestID

	w.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	w.wg.Wait()

	if got := onlyPausedRow(t, w, taskID).Status; got != "resumed" {
		t.Errorf("row status after the holder resumed it = %q, want resumed", got)
	}
	if run := runRowOf(t, w, onlyPausedRow(t, w, taskID).ExecutionID); run.Status != "held" || run.HeldBy != "" {
		t.Errorf("run = %s held by %q, want it still held by its request", run.Status, run.HeldBy)
	}
}

func TestRequest_DuplicateAnswerDuringTheHoldersClaimKeepsTheRequest(t *testing.T) {
	w, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-duplicate-answer"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	requestID := onlyPausedRow(t, w, taskID).RequestID
	w.resumeClaimHook = func() {
		w.resumeClaimHook = nil
		w.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	}

	w.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	w.wg.Wait()

	if acks.acks() != 0 {
		t.Errorf("a duplicate answer during this process's own claim acknowledged the run's request %d times, want 0", acks.acks())
	}
}

func TestRedelivery_RunWhoseHolderResumedAStepStartsOver(t *testing.T) {
	w, _ := newHILWorkflow(t, otherAgentNode())
	const taskID = "task-redelivered-holder-gone"
	insertPausedRow(t, w, taskID, "req-answered")
	insertPausedRow(t, w, taskID, "req-open")
	if _, claimed, err := w.pausedRepo.Claim(t.Context(), "req-answered", statusResumedByHolder); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)

	w.handleTaskRequest(t.Context(), *req, msg)

	if executionFor(w, taskID) == nil {
		t.Fatal("the run did not start over although the process that resumed its step is gone")
	}
	statuses, err := w.pausedRepo.StatusesFor(t.Context(), []string{"req-open"})
	if err != nil || statuses["req-open"] != statusClosedForRestart {
		t.Errorf("the earlier run's open pause = %v (err %v), want it closed", statuses, err)
	}
	if acks.acks() != 0 {
		t.Errorf("restarted run's request acknowledged %d times while it runs, want 0", acks.acks())
	}
}

func TestRedelivery_RowsClosedByAnEarlierRestartStartTheRunOver(t *testing.T) {
	w, _ := newHILWorkflow(t, otherAgentNode())
	const taskID = "task-restarted-twice"
	insertPausedRow(t, w, taskID, "req-a")
	if _, _, _, err := w.pausedRepo.CloseUnlessCarried(t.Context(), taskID, statusClosedForRestart, func([]wfstore.TaskPause) bool { return false }); err != nil {
		t.Fatalf("first restart close: %v", err)
	}
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)

	w.handleTaskRequest(t.Context(), *req, msg)

	if executionFor(w, taskID) == nil {
		t.Fatal("the request was dropped although no process carries the run")
	}
}

func TestRedelivery_AllRowsPausedClosesThemAndStartsOver(t *testing.T) {
	w, _ := newHILWorkflow(t, otherAgentNode())
	const taskID = "task-redelivered-parked"
	insertPausedRow(t, w, taskID, "req-a")
	insertPausedRow(t, w, taskID, "req-b")
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)

	w.handleTaskRequest(t.Context(), *req, msg)

	statuses, err := w.pausedRepo.StatusesFor(t.Context(), []string{"req-a", "req-b"})
	if err != nil || statuses["req-a"] != statusClosedForRestart || statuses["req-b"] != statusClosedForRestart {
		t.Errorf("earlier run's pauses after the redelivery = %v (err %v), want both closed", statuses, err)
	}
	exec := executionFor(w, taskID)
	if exec == nil {
		t.Fatal("the run did not start over")
	}
	subTaskOf(t, exec, "other")
	if acks.acks() != 0 {
		t.Errorf("restarted run's request acknowledged %d times while it runs, want 0", acks.acks())
	}
	w.answer(t.Context(), "req-a", "resumed", "answered", map[string]any{"approved": true})
	w.executionsMu.Lock()
	got := len(w.executionsByID)
	w.executionsMu.Unlock()
	if got != 1 {
		t.Errorf("an answer to a closed pause rebuilt the earlier run: %d executions, want 1", got)
	}
}

func TestRedelivery_NoRowsStartsOver(t *testing.T) {
	w, _ := newHILWorkflow(t, otherAgentNode())
	const taskID = "task-redelivered-fresh"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)

	w.handleTaskRequest(t.Context(), *req, msg)

	exec := executionFor(w, taskID)
	if exec == nil {
		t.Fatal("a redelivered request with no paused rows did not start its run")
	}
	subTaskOf(t, exec, "other")
	if acks.acks() != 0 {
		t.Errorf("acks=%d, want the request held by the running run", acks.acks())
	}
}

func TestRedelivery_ResidentRunKeepsTheNewDelivery(t *testing.T) {
	w, _ := newHILWorkflow(t, otherAgentNode())
	const taskID = "task-resident"
	var first, second ackCounter
	req, msg := deliveredRequest(taskID, &first, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	exec := executionFor(w, taskID)

	req, msg = deliveredRequest(taskID, &second, true)
	w.handleTaskRequest(t.Context(), *req, msg)

	if got := executionFor(w, taskID); got != exec {
		t.Fatal("the redelivery replaced the run still resident here")
	}
	if first.acks() != 0 || second.acks() != 0 {
		t.Fatalf("acks first=%d second=%d while the run runs, want 0 and 0", first.acks(), second.acks())
	}
	deliverSubTaskResult(t, w, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)
	if first.acks() != 0 || second.acks() != 1 {
		t.Errorf("acks at run end first=%d second=%d, want the live delivery acknowledged", first.acks(), second.acks())
	}
}

func removalCards(msgs []capturedMessage) int {
	n := 0
	for _, m := range msgs {
		if m.UserProperties["removed"] == "true" {
			n++
		}
	}
	return n
}

func TestStopForProcessExit_LeavesRunningRunAtDeadline(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "process-exit-deadline",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	}
	w, brk := startedWorkflowInstance(t, cfg)
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	var acks ackCounter
	req, msg := deliveredRequest("019e0000-0000-7000-8000-0000000a0002", &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)

	const budget = 600 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	started := time.Now()
	if err := w.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}

	if waited := time.Since(started); waited < budget-50*time.Millisecond {
		t.Errorf("returned after %s with a run still going, want it to wait out the %s budget", waited, budget)
	}
	msgs := brk.snapshot()
	if acks.acks() != 0 {
		t.Errorf("running run's request acknowledged %d times at the deadline, want 0", acks.acks())
	}
	if finalTaskOn(t, msgs, requestAckReplyTo) != nil {
		t.Error("the caller was answered at the deadline")
	}
	if got := cancelRequestsTo(msgs, a2a.AgentRequestTopic("test-ns", "stepAgent")); got != 0 {
		t.Errorf("%d tasks/cancel sent to the running step, want 0", got)
	}
	if got := removalCards(msgs); got != 0 {
		t.Errorf("%d removal cards published on process exit, want 0", got)
	}
}

func TestStopForProcessExit_RunCutByTheExitIsNotAnsweredOrAcknowledged(t *testing.T) {
	brk := &capturingBroker{}
	w := NewWorkflowInstance(&WorkflowConfig{
		Name:      "process-exit-cut",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	})
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	startCtx, cancelStart := context.WithCancel(t.Context())
	defer cancelStart()
	if err := w.Start(startCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	const taskID = "019e0000-0000-7000-8000-0000000a0004"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	exec := executionFor(w, taskID)
	stepSubTask := subTaskOf(t, exec, "step")

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := w.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}
	cancelStart()
	_ = exec.Executor.Advance(t.Context(), &EventNodeFailed{NodeID: "step", SubTaskID: stepSubTask, Err: context.Canceled})

	if acks.acks() != 0 {
		t.Errorf("request of the run cut by the exit acknowledged %d times, want 0", acks.acks())
	}
	if finalTaskOn(t, brk.snapshot(), requestAckReplyTo) != nil {
		t.Error("the caller was answered for a run cut by the exit")
	}
}

func TestStopForProcessExit_ReturnsOnceTheRunEnds(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "process-exit-drain",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	}
	w, brk := startedWorkflowInstance(t, cfg)
	var acks ackCounter
	req, msg := deliveredRequest("019e0000-0000-7000-8000-0000000a0003", &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	stepSubTask := requestIDSentTo(t, brk.snapshot(), a2a.AgentRequestTopic("test-ns", "stepAgent"))

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	started := time.Now()
	go func() { stopped <- w.StopForProcessExit(ctx) }()
	select {
	case <-w.requestIntakeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the stop never stopped reading the request queue")
	}
	deliverSubTaskResult(t, w, stepSubTask, a2a.TaskStateCompleted)

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("StopForProcessExit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StopForProcessExit did not return after the last run ended")
	}
	if waited := time.Since(started); waited > 5*time.Second {
		t.Errorf("drain took %s, want it to end with the run", waited)
	}
	msgs := brk.snapshot()
	if final := finalTaskOn(t, msgs, requestAckReplyTo); final == nil || final.Status.State != a2a.TaskStateCompleted {
		t.Errorf("final response = %+v, want the run's own completion", final)
	}
	if acks.acks() != 1 {
		t.Errorf("request acknowledged %d times, want 1 after the run ended", acks.acks())
	}
	if got := removalCards(msgs); got != 0 {
		t.Errorf("%d removal cards published on process exit, want 0", got)
	}
}

func TestStopForProcessExit_ParkedRunDoesNotHoldTheExit(t *testing.T) {
	w, _ := newHILWorkflow(t, gatedCallNode())
	const taskID = "task-parked-exit"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	if acks.acks() != 1 {
		t.Fatalf("parked run's request acks=%d, want 1", acks.acks())
	}
	if executionFor(w, taskID) != nil {
		t.Fatal("a parked run stayed in memory; its state is in workflow_checkpoint")
	}
	if run := runRowOf(t, w, onlyPausedRow(t, w, taskID).ExecutionID); run.Status != "parked" {
		t.Fatalf("run status = %q, want parked before the exit", run.Status)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	if err := w.StopForProcessExit(ctx); err != nil {
		t.Fatalf("StopForProcessExit: %v", err)
	}

	if waited := time.Since(started); waited > 2*time.Second {
		t.Errorf("exit waited %s on a run parked for a human, want it to return at once", waited)
	}
}

func TestHealth_RequestReceiveLoopLossNeedsRestart(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	w := NewWorkflowInstance(&WorkflowConfig{
		Name:      "health-intake",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	})
	if err := w.Init(t.Context(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	if err := w.Health(); err != nil {
		t.Fatalf("Health after start: %v", err)
	}

	if err := brk.DeleteQueue(w.queue.Name()); err != nil {
		t.Fatalf("delete request queue: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var err error
	for err = w.Health(); err == nil && time.Now().Before(deadline); err = w.Health() {
		time.Sleep(20 * time.Millisecond)
	}
	if !errors.Is(err, runtime.ErrRestartRequired) {
		t.Errorf("Health after the request receive loop died = %v, want runtime.ErrRestartRequired", err)
	}
}
