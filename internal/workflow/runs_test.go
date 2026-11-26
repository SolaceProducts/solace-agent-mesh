// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/credentials"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func newPeer(t *testing.T, of *WorkflowInstance) (*WorkflowInstance, *approvalArgTool, broker.Broker) {
	t.Helper()
	p, brk := newHILInstance(t)
	p.cfg, p.dag = of.cfg, of.dag
	p.pausedDB, p.pausedRepo = of.pausedDB, of.pausedRepo
	rec := newApprovalArgTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	p.tools = ts
	p.hilToolConfigs = gatedCallConfig()
	ts.SetHILConfigs(p.hilToolConfigs)
	p.timerManager = NewInProcessTimerManager(slog.Default(), p.fireTimerEvent)
	go p.timerManager.Start(p.ctx)
	return p, rec, brk
}

func subscribed(t *testing.T, brk broker.Broker, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue("q-"+strings.ReplaceAll(topic, "/", "-"), broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func receiveOn(t *testing.T, brk broker.Broker, q broker.Queue) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		return nil
	}
	return msg
}

func finalState(t *testing.T, brk broker.Broker, q broker.Queue) a2a.TaskState {
	t.Helper()
	msg := receiveOn(t, brk, q)
	if msg == nil {
		return ""
	}
	var resp struct {
		Result *a2a.Task `json:"result"`
	}
	if err := json.Unmarshal(msg.Payload, &resp); err != nil || resp.Result == nil {
		t.Fatalf("not a final task response: %s", msg.Payload)
	}
	return resp.Result.Status.State
}

func answerFromQueue(w *WorkflowInstance, requestID string, approved bool, acks *ackCounter, redelivered bool) *broker.Message {
	payload, _ := json.Marshal(a2a.UserInputResponseData{
		Type: string(a2a.SignalUserInputResponse), RequestID: requestID, Status: "answered",
		Data: map[string]any{"approved": approved},
	})
	msg := broker.NewDeliveredMessage("a-"+requestID, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID),
		payload, func() { acks.n.Add(1) }, func() {})
	msg.Redelivered = redelivered
	return msg
}

func afterNode() *NodeConfig {
	return &NodeConfig{ID: "after", Type: "agent", AgentName: "afterAgent", DependsOn: []string{"call_gated"}}
}

func parkedTask(t *testing.T, w *WorkflowInstance, taskID string) (requestID, executionID string) {
	t.Helper()
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	pause := onlyPausedRow(t, w, taskID)
	if run := runRowOf(t, w, pause.ExecutionID); run.Status != "parked" || acks.acks() != 1 {
		t.Fatalf("run = %s with %d request acks, want parked and acknowledged", run.Status, acks.acks())
	}
	if executionFor(w, taskID) != nil {
		t.Fatal("the parked run stayed in memory")
	}
	return pause.RequestID, pause.ExecutionID
}

func TestRun_AnswerOnAnotherAWETakesTheParkedRunOver(t *testing.T) {
	a, recA := newHILWorkflow(t, gatedCallNode())
	b, recB, brkB := newPeer(t, a)
	replies := subscribed(t, brkB, requestAckReplyTo)
	requestID, executionID := parkedTask(t, a, "task-takeover")

	var acks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, requestID, true, &acks, false))
	b.wg.Wait()

	if !recB.executed() || recA.executed() {
		t.Fatalf("tool ran on the AWE that took the answer: %v, on the one that parked: %v; want only the first", recB.executed(), recA.executed())
	}
	if got := finalState(t, brkB, replies); got != a2a.TaskStateCompleted {
		t.Errorf("final state = %q, want completed", got)
	}
	if run := runRowOf(t, b, executionID); run.Status != "ended" || acks.acks() != 1 {
		t.Errorf("run = %s with %d answer acks, want ended and the answer acknowledged once", run.Status, acks.acks())
	}
}

func TestRun_AnswerRedeliveredAfterItsAWEDiedContinuesTheRun(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode(), afterNode())
	b, recB, _ := newPeer(t, a)
	requestID, executionID := parkedTask(t, a, "task-holder-died")
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, requestID, true, &bAcks, false))
	b.wg.Wait()
	if !recB.executed() || bAcks.acks() != 0 {
		t.Fatalf("first AWE: tool ran=%v answer acks=%d, want the run continued and the answer held", recB.executed(), bAcks.acks())
	}

	duplicate, recDup, _ := newPeer(t, a)
	var dupAcks ackCounter
	duplicate.handleWorkflowHILResponse(t.Context(), answerFromQueue(duplicate, requestID, true, &dupAcks, false))
	duplicate.wg.Wait()
	if recDup.executed() || dupAcks.acks() != 1 {
		t.Errorf("duplicate: tool ran=%v acks=%d, want dropped and acknowledged", recDup.executed(), dupAcks.acks())
	}

	c, recC, _ := newPeer(t, a)
	var cAcks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, requestID, true, &cAcks, true))
	c.wg.Wait()
	if !recC.executed() || cAcks.acks() != 0 {
		t.Errorf("redelivered: tool ran=%v acks=%d, want the run continued and the answer held", recC.executed(), cAcks.acks())
	}
	if c.residentByID(executionID) == nil {
		t.Error("the run is not executing on the AWE the answer was redelivered to")
	}
	if run := runRowOf(t, c, executionID); run.Status != "held" || run.HeldBy != requestID {
		t.Errorf("run = %s held by %q, want held by the answer", run.Status, run.HeldBy)
	}
}

func TestRun_AnswerRedeliveredWithCheckpointsContinuesFromTheLastStep(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode(), afterNode())
	a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	requestID, executionID := parkedTask(t, a, "task-holder-died-checkpointed")

	b, recB, brkB := newPeer(t, a)
	b.checkpointRepo = a.checkpointRepo
	afterB := subscribed(t, brkB, a2a.AgentRequestTopic(b.cfg.Namespace, "afterAgent"))
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, requestID, true, &bAcks, false))
	b.wg.Wait()
	if !recB.executed() || receiveOn(t, brkB, afterB) == nil {
		t.Fatalf("first AWE: tool ran=%v, want the run continued to the step after the approval", recB.executed())
	}
	if run := runRowOf(t, b, executionID); run.HeldBy != requestID || slices.Contains(mustOpenPauses(t, run), requestID) {
		t.Fatalf("run after the approved step = held by %q with open pauses %s, want held by the answer and the pause applied", run.HeldBy, run.OpenPauses)
	}

	c, recC, brkC := newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	afterC := subscribed(t, brkC, a2a.AgentRequestTopic(c.cfg.Namespace, "afterAgent"))
	var cAcks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, requestID, true, &cAcks, true))
	c.wg.Wait()
	if recC.executed() {
		t.Error("the approved tool ran again on the AWE the answer was redelivered to")
	}
	if receiveOn(t, brkC, afterC) == nil {
		t.Error("the step in flight at the death was not dispatched again")
	}
	if cAcks.acks() != 0 || c.residentByID(executionID) == nil {
		t.Errorf("redelivered answer acks=%d, run resident=%v; want the answer holding the run here", cAcks.acks(), c.residentByID(executionID) != nil)
	}
}

func mustOpenPauses(t *testing.T, run *wfstore.WorkflowRun) []string {
	t.Helper()
	ids, err := run.OpenPauseIDs()
	if err != nil {
		t.Fatalf("open pauses: %v", err)
	}
	return ids
}

func TestRun_RequestRedeliveredForAParkedRunIsDropped(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	b, recB, _ := newPeer(t, a)
	requestID, executionID := parkedTask(t, a, "task-parked-redelivered")

	var acks ackCounter
	req, msg := deliveredRequest("task-parked-redelivered", &acks, true)
	b.handleTaskRequest(t.Context(), *req, msg)
	b.wg.Wait()

	if acks.acks() != 1 || executionFor(b, "task-parked-redelivered") != nil || recB.executed() {
		t.Errorf("redelivered request: acks=%d started=%v, want it dropped", acks.acks(), executionFor(b, "task-parked-redelivered") != nil)
	}
	if run := runRowOf(t, b, executionID); run.Status != "parked" {
		t.Errorf("run = %s, want still parked", run.Status)
	}
	if pause := onlyPausedRow(t, b, "task-parked-redelivered"); pause.ExecutionID != executionID {
		t.Errorf("pause belongs to execution %s, want the parked run's", pause.ExecutionID)
	}
	if status, err := b.pausedRepo.StatusesFor(t.Context(), []string{requestID}); err != nil || status[requestID] != "paused" {
		t.Errorf("pause = %v (%v), want still paused", status, err)
	}
}

func TestRun_RequestRedeliveredForARunItHeldStartsOver(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	b, _, _ := newPeer(t, a)
	const taskID = "task-held-redelivered"
	var aAcks ackCounter
	req, msg := deliveredRequest(taskID, &aAcks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	first := onlyPausedRow(t, a, taskID)
	if run := runRowOf(t, a, first.ExecutionID); run.Status != "held" || aAcks.acks() != 0 {
		t.Fatalf("run = %s with %d acks, want held by its request while the other step runs", run.Status, aAcks.acks())
	}

	var bAcks ackCounter
	req, msg = deliveredRequest(taskID, &bAcks, true)
	b.handleTaskRequest(t.Context(), *req, msg)
	b.wg.Wait()

	if executionFor(b, taskID) == nil {
		t.Fatal("the redelivered request did not start the run over")
	}
	if run := runRowOf(t, b, first.ExecutionID); run.Status != "ended" {
		t.Errorf("the dead holder's run = %s, want ended", run.Status)
	}
	if status, _ := b.pausedRepo.StatusesFor(t.Context(), []string{first.RequestID}); status[first.RequestID] != statusClosedForRestart {
		t.Errorf("the dead holder's pause = %q, want %s", status[first.RequestID], statusClosedForRestart)
	}
}

func TestRun_DeadlineOfAParkedRunEndsItOnTheAWEThatTakesIt(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	b, recB, brkB := newPeer(t, a)
	deadlines := subscribed(t, a.svc.Broker(), a2a.WorkflowDeadlineSubscription("test-ns", "wf"))
	replies := subscribed(t, brkB, requestAckReplyTo)
	_, executionID := parkedTask(t, a, "task-deadline")

	a.fireTimerEvent(executionID, &EventWorkflowTimeout{})
	published := receiveOn(t, a.svc.Broker(), deadlines)
	if published == nil || published.Topic != a2a.WorkflowDeadlineTopic("test-ns", "wf", executionID) {
		t.Fatalf("deadline message = %+v, want one on the run's deadline topic", published)
	}

	var acks ackCounter
	b.handleDeadline(t.Context(), broker.NewDeliveredMessage("d", published.Topic, published.Payload, func() { acks.n.Add(1) }, func() {}))
	b.wg.Wait()

	if recB.executed() {
		t.Error("the timed-out run dispatched its gated tool")
	}
	if got := finalState(t, brkB, replies); got == "" || got == a2a.TaskStateCompleted {
		t.Errorf("final state = %q, want the workflow timeout's", got)
	}
	if run := runRowOf(t, b, executionID); run.Status != "ended" || acks.acks() != 1 {
		t.Errorf("run = %s with %d acks, want ended and the deadline acknowledged", run.Status, acks.acks())
	}
}

func TestRun_CancelOfAParkedRunEndsItOnTheAWEThatTakesIt(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	b, recB, brkB := newPeer(t, a)
	replies := subscribed(t, brkB, requestAckReplyTo)
	requestID, executionID := parkedTask(t, a, "task-cancel-parked")

	var acks ackCounter
	cancel := broker.NewDeliveredMessage("c", "", nil, func() { acks.n.Add(1) }, func() {})
	b.handleCancelRequest(t.Context(), a2a.Request{Method: "tasks/cancel", Params: map[string]any{"id": "task-cancel-parked"}}, cancel)
	b.wg.Wait()
	b.drainRetirements()

	if recB.executed() {
		t.Error("the cancelled run dispatched its gated tool")
	}
	if got := finalState(t, brkB, replies); got != a2a.TaskStateCancelled {
		t.Errorf("final state = %q, want canceled", got)
	}
	if run := runRowOf(t, b, executionID); run.Status != "ended" || acks.acks() != 1 {
		t.Errorf("run = %s with %d acks, want ended and the cancel acknowledged", run.Status, acks.acks())
	}
	if status, _ := b.pausedRepo.StatusesFor(t.Context(), []string{requestID}); status[requestID] == "paused" {
		t.Error("the cancelled run's pause is still answerable")
	}
}

func TestRun_ParkWriteFailureKeepsTheRequestAndRetries(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	if _, err := a.pausedDB.ExecContext(t.Context(),
		`CREATE TRIGGER fail_park BEFORE UPDATE ON workflow_checkpoint BEGIN SELECT RAISE(ABORT, 'store down'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	var acks ackCounter
	req, msg := deliveredRequest("task-park-retry", &acks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	if acks.acks() != 0 || executionFor(a, "task-park-retry") == nil {
		t.Fatalf("with the park write failing: acks=%d resident=%v, want the request held and the run kept", acks.acks(), executionFor(a, "task-park-retry") != nil)
	}

	if _, err := a.pausedDB.ExecContext(t.Context(), `DROP TRIGGER fail_park`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for acks.acks() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if acks.acks() != 1 || executionFor(a, "task-park-retry") != nil {
		t.Errorf("after the store recovered: acks=%d resident=%v, want parked and acknowledged", acks.acks(), executionFor(a, "task-park-retry") != nil)
	}
}

func identitySealer(t *testing.T, key string) auth.CredentialService {
	t.Helper()
	km, err := identity.NewKeyManager(identity.Config{Mode: "manual", Key: key}, "agent", nil, nil)
	if err != nil {
		t.Fatalf("KeyManager: %v", err)
	}
	return credentials.NewAuthAdapter(credentials.NewSessionStore(session.NewMemoryStore(), km, nil))
}

func TestRun_CallerTokenIsStoredSealedAndRestored(t *testing.T) {
	const token = "eyJ.caller-token.sig"
	const key = "primary-key-1234567890ab"
	w, brk := newHILInstance(t)
	w.dag = &DependencyGraph{}
	w.svc = runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithCredentialService(identitySealer(t, key))).Services()
	state := NewExecutionState("wf", "exec-token")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "task-token", "ctx-1", nil, a2a.UserProperties{UserID: "alice", AuthToken: token}, false)
	execCtx.Identity = &auth.Identity{UserID: "alice", AuthToken: token}
	e := &DAGExecutor{cfg: w.cfg, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	if err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: "req-token",
		Surface: json.RawMessage(`{"surfaceId":"s"}`), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}

	rebuilt := parkAndRebuild(t, w, e, nil)

	pause, err := w.pausedRepo.Get(t.Context(), "req-token")
	if err != nil || pause == nil {
		t.Fatalf("read pause: %v", err)
	}
	for where, stored := range map[string]string{"run row": runRowOf(t, w, "exec-token").ExecContext, "pause": pause.ExecContext} {
		if strings.Contains(stored, token) {
			t.Errorf("the %s stores the caller's token in plain text", where)
		}
	}
	if rebuilt.Context.UserProps.AuthToken != token || rebuilt.Context.Identity == nil || rebuilt.Context.Identity.AuthToken != token {
		t.Errorf("rebuilt run's token = %q / %+v, want the caller's token in both places", rebuilt.Context.UserProps.AuthToken, rebuilt.Context.Identity)
	}

	w.svc = runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithCredentialService(identitySealer(t, "different-key-abcdef12345678"))).Services()
	other, err := w.rebuildFromRun(runRowOf(t, w, "exec-token"), nil, "")
	if err != nil {
		t.Fatalf("rebuild with another key: %v", err)
	}
	if other.Context.UserProps.AuthToken != "" {
		t.Error("an AWE with another identity key resumed the run with a token")
	}
}

func TestStopForReplace_LeavesRunningRunForTheNewCopy(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "replace-handover",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	}
	w, brk := startedWorkflowInstance(t, cfg)
	var acks ackCounter
	req, msg := deliveredRequest("019e0000-0000-7000-8000-0000000a0099", &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)

	const budget = 600 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	started := time.Now()
	if err := w.StopForReplace(ctx); err != nil {
		t.Fatalf("StopForReplace: %v", err)
	}

	if waited := time.Since(started); waited < budget-50*time.Millisecond {
		t.Errorf("returned after %s with a run still going, want it to wait out the %s budget", waited, budget)
	}
	msgs := brk.snapshot()
	if acks.acks() != 0 {
		t.Errorf("running run's request acknowledged %d times, want 0", acks.acks())
	}
	if finalTaskOn(t, msgs, requestAckReplyTo) != nil {
		t.Error("the caller was answered by the copy being replaced")
	}
	if got := cancelRequestsTo(msgs, a2a.AgentRequestTopic("test-ns", "stepAgent")); got != 0 {
		t.Errorf("%d tasks/cancel sent to the running step, want 0", got)
	}
	if got := removalCards(msgs); got != 1 {
		t.Errorf("%d removal cards, want 1", got)
	}
}

func TestRun_AnswerWrittenWhileRunningIsAppliedAtThePark(t *testing.T) {
	w, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-answer-at-park"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	exec := executionFor(w, taskID)
	requestID := onlyPausedRow(t, w, taskID).RequestID
	answer := `{"type":"user_input_response","request_id":"` + requestID + `","status":"answered","data":{"approved":true}}`
	if res, err := w.runStore().AcceptAnswer(t.Context(), w.cfg.Name, requestID, "resumed", answer); err != nil || res.Outcome != wfstore.AnswerRecorded {
		t.Fatalf("another AWE recording the answer: outcome=%v err=%v", res.Outcome, err)
	}

	deliverSubTaskResult(t, w, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)

	deadline := time.Now().Add(5 * time.Second)
	for acks.acks() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !rec.executed() {
		t.Error("the written answer was not applied when the run reached its park")
	}
	if acks.acks() != 1 || runRowOf(t, w, exec.State.ExecutionID).Status != "ended" {
		t.Errorf("acks=%d run=%s, want the run finished on its own request", acks.acks(), runRowOf(t, w, exec.State.ExecutionID).Status)
	}
}

func TestStop_KeepsTheRequestOfARunNotParked(t *testing.T) {
	w, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	var acks ackCounter
	req, msg := deliveredRequest("task-stop-held", &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()

	w.failInFlightExecutions()
	w.settleRequestsLeftOnStop()

	if acks.acks() != 0 {
		t.Errorf("request acknowledged %d times on stop, want 0: the run is not parked", acks.acks())
	}
}

type cutTool struct{ *schemaTool }

func (cutTool) Execute(ctx context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestStopForReplace_ToolCutByTheReplaceDoesNotEndTheRun(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "replace-tool",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "slow", Type: "tool", ToolName: "slow"}},
	}
	w, brk := startedWorkflowInstance(t, cfg)
	w.tools = toolSetWith(t, cutTool{&schemaTool{name: "slow"}})
	var acks ackCounter
	req, msg := deliveredRequest("019e0000-0000-7000-8000-0000000a009a", &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if err := w.StopForReplace(ctx); err != nil {
		t.Fatalf("StopForReplace: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if acks.acks() != 0 {
		t.Errorf("request acknowledged %d times, want 0", acks.acks())
	}
	if final := finalTaskOn(t, brk.snapshot(), requestAckReplyTo); final != nil {
		t.Errorf("the replaced copy answered the caller with %q", final.Status.State)
	}
}

func TestRegisterExecution_IndexesTheRebuiltRunsSubTasks(t *testing.T) {
	w, _ := newHILInstance(t)
	state := NewExecutionState("wf", "exec-index")
	state.AddPending("ask")
	state.TrackSubTask("sub-in-flight", "ask")
	execCtx := NewExecutionContext(w.ctx, "task-index", "ctx", nil, a2a.UserProperties{}, false)
	t.Cleanup(execCtx.Cancel)
	exec := &WorkflowExecution{State: state, Context: execCtx, Executor: &DAGExecutor{state: state, execCtx: execCtx}}

	w.registerExecution("exec-index", exec)

	if got, node := w.findExecutionForSubTask("sub-in-flight"); got != exec || node != "ask" {
		t.Errorf("findExecutionForSubTask = %v/%q, want the rebuilt run and its node", got, node)
	}
}
