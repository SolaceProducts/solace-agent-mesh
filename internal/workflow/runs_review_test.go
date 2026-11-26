// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func TestRun_DeadlineTimerOfAParkedRunPublishesItsDeadline(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	a.cfg.WorkflowTimeout = 300 * time.Millisecond
	deadlines := subscribed(t, a.svc.Broker(), a2a.WorkflowDeadlineSubscription("test-ns", "wf"))
	_, executionID := parkedTask(t, a, "task-deadline-timer")

	published := receiveOn(t, a.svc.Broker(), deadlines)
	if published == nil || published.Topic != a2a.WorkflowDeadlineTopic("test-ns", "wf", executionID) {
		t.Fatalf("deadline message = %+v, want the parked run's deadline published by its timer", published)
	}
}

func hidePausesOnce(t *testing.T, w *WorkflowInstance, restoreAfter time.Duration) func() {
	t.Helper()
	var once sync.Once
	return func() {
		once.Do(func() {
			if _, err := w.pausedDB.ExecContext(t.Context(), `ALTER TABLE workflow_paused RENAME TO workflow_paused_hidden`); err != nil {
				t.Errorf("hide pauses: %v", err)
				return
			}
			restore := func() {
				_, _ = w.pausedDB.ExecContext(context.Background(), `ALTER TABLE workflow_paused_hidden RENAME TO workflow_paused`)
			}
			if restoreAfter > 0 {
				time.AfterFunc(restoreAfter, restore)
			} else {
				t.Cleanup(restore)
			}
		})
	}
}

func TestRun_AnswerWhoseClaimFailedIsAppliedWhenTheRunParksAgain(t *testing.T) {
	a, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-claim-failed"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	pause := onlyPausedRow(t, a, taskID)

	b, _, _ := newPeer(t, a)
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, pause.RequestID, true, &bAcks, false))
	if rec.executed() || pauseStatus(t, a, pause.RequestID) != "answered" {
		t.Fatalf("tool ran=%v, pause %s; want the answer written and not applied", rec.executed(), pauseStatus(t, a, pause.RequestID))
	}

	a.resumeClaimHook = hidePausesOnce(t, a, 300*time.Millisecond)
	deliverSubTaskResult(t, a, subTaskOf(t, executionFor(a, taskID), "other"), a2a.TaskStateCompleted)

	waitForCondition(t, 5*time.Second, rec.executed, "the answer whose claim failed was never applied")
}

func TestRun_UnreadableOpenPausesReturnTheAnswerForItsRedelivery(t *testing.T) {
	a, _ := newHILWorkflow(t, twoGatesNodes()...)
	a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	const taskID = "task-unreadable-pauses"
	var reqAcks ackCounter
	req, msg := deliveredRequest(taskID, &reqAcks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	g1 := pauseOfNode(t, a, taskID, "g1")

	b, recB, _ := newPeer(t, a)
	b.checkpointRepo = a.checkpointRepo
	b.resumeClaimHook = hidePausesOnce(t, b, 0)
	var acks, nacks ackCounter
	answer := answerFromQueue(b, g1.RequestID, true, &acks, false)
	answer = broker.NewDeliveredMessage("a-"+g1.RequestID, answer.Topic, answer.Payload,
		func() { acks.n.Add(1) }, func() { nacks.n.Add(1) })
	b.handleWorkflowHILResponse(t.Context(), answer)
	b.wg.Wait()

	if recB.calls() != 0 {
		t.Errorf("tool ran %d times, want none until the run's pauses are known", recB.calls())
	}
	if b.residentByID(g1.ExecutionID) != nil {
		t.Error("the run was continued without its open pauses")
	}
	if acks.acks() != 0 || nacks.acks() != 1 {
		t.Errorf("answer acks=%d nacks=%d, want it returned to the queue once", acks.acks(), nacks.acks())
	}
	if run := runRowOf(t, b, g1.ExecutionID); run.Status != "held" || run.HeldBy != g1.RequestID {
		t.Fatalf("run = %s held by %q, want held by the answer", run.Status, run.HeldBy)
	}

	if _, err := b.pausedDB.ExecContext(t.Context(), `ALTER TABLE workflow_paused_hidden RENAME TO workflow_paused`); err != nil {
		t.Fatalf("restore pauses: %v", err)
	}
	c, recC, _ := newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	var cAcks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, g1.RequestID, true, &cAcks, true))
	c.wg.Wait()
	if recC.calls() != 1 || c.residentByID(g1.ExecutionID) == nil {
		t.Errorf("redelivered: %d tool calls, resident=%v; want the approved step run once and the run continued", recC.calls(), c.residentByID(g1.ExecutionID) != nil)
	}
	if pauses, err := c.pausedRepo.PausesForTask(t.Context(), taskID); err != nil || len(pauses) != 2 {
		t.Errorf("pauses of the task = %d (%v), want the two first asked: g2 not asked again", len(pauses), err)
	}
}

func TestAnswer_StopLandingDuringTheClaimLeavesTheAnswerOnThePause(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.w.resumeClaimHook = f.w.failInFlightExecutions

	f.w.answer(t.Context(), hilStopRequestID, "resumed", "approved", nil)

	if got := pauseStatus(t, f.w, hilStopRequestID); got != "answered" {
		t.Errorf("pause = %s, want the answer back on it", got)
	}
	f.exec.Executor.mu.Lock()
	stillPending := f.exec.State.PendingNodes["approve"]
	f.exec.Executor.mu.Unlock()
	if !stillPending {
		t.Error("the answer claimed during the stop advanced the paused step")
	}
	f.requireNoTerminal(t)
}

func startedHILInstance(t *testing.T, brk broker.Broker, dbURL string) *WorkflowInstance {
	t.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: "workflow_hil_pause_resume", Default: true})
	w := NewWorkflowInstance(&WorkflowConfig{
		Name: "wf", Namespace: "test-ns", HILDatabaseURL: dbURL,
		Nodes: []*NodeConfig{
			{ID: "approve", Type: "agent", AgentName: "approver"},
			{ID: "next", Type: "agent", AgentName: "nextAgent", DependsOn: []string{"approve"}},
			{ID: "other", Type: "agent", AgentName: "otherAgent"},
		},
	})
	if err := w.Init(t.Context(), nil, &stubServices{brk: brk, features: reg, logger: slog.Default()}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	return w
}

func pausedRunOn(t *testing.T, w *WorkflowInstance, executionID, taskID, requestID string, pending ...string) *DAGExecutor {
	t.Helper()
	state := NewExecutionState("wf", executionID)
	state.AddPending("approve")
	for _, id := range pending {
		state.AddPending(id)
	}
	execCtx := NewExecutionContext(w.ctx, taskID, "ctx-1", nil,
		a2a.UserProperties{UserID: "alice", ReplyTo: requestAckReplyTo}, false)
	e := &DAGExecutor{cfg: w.cfg, dag: w.dag, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	w.registerExecution(executionID, &WorkflowExecution{State: state, Context: execCtx, Executor: e})
	e.mu.Lock()
	err := w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-" + executionID, RequestID: requestID,
		Surface:   json.RawMessage(`{"surfaceId":"s"}`),
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	parked := len(pending) == 0 && e.writeParkLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if parked {
		w.evictParked(e)
	}
	return e
}

func answerPayload(t *testing.T, requestID string) []byte {
	t.Helper()
	payload, err := json.Marshal(a2a.UserInputResponseData{
		Type: string(a2a.SignalUserInputResponse), RequestID: requestID, Status: "answered",
	})
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	return payload
}

func runStatusIs(w *WorkflowInstance, executionID, status string) func() bool {
	return func() bool {
		run, err := w.runStore().Get(context.Background(), executionID)
		return err == nil && run != nil && run.Status == status
	}
}

func TestQueue_AnswersAndDeadlinesReachAStartedInstance(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	w := startedHILInstance(t, brk, "sqlite:///"+filepath.Join(t.TempDir(), "wf.db"))
	next := subscribed(t, brk, a2a.AgentRequestTopic("test-ns", "nextAgent"))

	pausedRunOn(t, w, "exec-answered", "task-answered", "req-answered")
	pausedRunOn(t, w, "exec-deadline", "task-deadline", "req-deadline")

	if err := brk.PublishGuaranteed(t.Context(), a2a.NotifyResponseTopic("test-ns", "wf", "req-answered"),
		&broker.Message{Payload: answerPayload(t, "req-answered")}); err != nil {
		t.Fatalf("publish answer: %v", err)
	}
	if err := brk.PublishGuaranteed(t.Context(), a2a.WorkflowDeadlineTopic("test-ns", "wf", "exec-deadline"),
		&broker.Message{Payload: []byte("exec-deadline")}); err != nil {
		t.Fatalf("publish deadline: %v", err)
	}

	if receiveOn(t, brk, next) == nil {
		t.Error("the answer taken from the queue did not continue the run to its next step")
	}
	waitForCondition(t, 5*time.Second, runStatusIs(w, "exec-deadline", "ended"),
		"the deadline taken from the queue did not end the parked run")
}

func TestQueue_AnswerPassedOnIsAppliedByTheAWERunningTheRun(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	dbURL := "sqlite:///" + filepath.Join(t.TempDir(), "wf.db")
	running := startedHILInstance(t, brk, dbURL)
	taking := startedHILInstance(t, brk, dbURL)
	next := subscribed(t, brk, a2a.AgentRequestTopic("test-ns", "nextAgent"))

	pausedRunOn(t, running, "exec-passed-on", "task-passed-on", "req-passed-on", "other")

	var acks ackCounter
	taking.handleWorkflowHILResponse(t.Context(), broker.NewDeliveredMessage("a",
		a2a.NotifyResponseTopic("test-ns", "wf", "req-passed-on"), answerPayload(t, "req-passed-on"),
		func() { acks.n.Add(1) }, func() {}))

	if receiveOn(t, brk, next) == nil {
		t.Error("the answer passed on was not applied by the AWE running the run")
	}
	if acks.acks() != 1 {
		t.Errorf("answer acks = %d, want 1 once it is written on the pause", acks.acks())
	}
}

func TestRun_SiblingStepDoesNotParkTheRunOverAClaimedAnswer(t *testing.T) {
	a, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-claim-park-race"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	paused := onlyPausedRow(t, a, taskID)
	exec := executionFor(a, taskID)

	pause, claimed, err := a.runStore().ClaimHeld(t.Context(), paused.RequestID, runRowOf(t, a, paused.ExecutionID).Holder(), "resumed")
	if err != nil || !claimed {
		t.Fatalf("ClaimHeld = %v, %v", claimed, err)
	}
	deliverSubTaskResult(t, a, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)
	if run := runRowOf(t, a, paused.ExecutionID); run.Status != "held" || acks.acks() != 0 {
		t.Fatalf("run = %s with %d request acks, want still held while its claimed answer is applied", run.Status, acks.acks())
	}

	a.applyAnswer(t.Context(), exec, pause, a2a.UserInputResponseData{
		RequestID: paused.RequestID, Status: "answered", Data: map[string]any{"approved": true},
	})
	a.wg.Wait()
	if !rec.executed() || !finalized(exec) || acks.acks() != 1 {
		t.Errorf("tool ran=%v finalized=%v acks=%d, want the approved step run and the run ended", rec.executed(), finalized(exec), acks.acks())
	}
}

func TestRun_RestoreUnderAnAnswerNeverAppliedAsksNoOneAgain(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode(), afterNode())
	a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	const taskID = "task-answer-never-applied"
	requestID, executionID := parkedTask(t, a, taskID)
	if res, err := a.runStore().AcceptAnswer(t.Context(), a.cfg.Name, requestID, "resumed", `{}`); err != nil || res.Outcome != wfstore.AnswerTookOver {
		t.Fatalf("AcceptAnswer = %v, %v; want the run taken over", res.Outcome, err)
	}

	c, recC, brkC := newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	afterC := subscribed(t, brkC, a2a.AgentRequestTopic(c.cfg.Namespace, "afterAgent"))
	var acks ackCounter
	c.handleWorkflowHILResponse(t.Context(), answerFromQueue(c, requestID, true, &acks, true))
	c.wg.Wait()

	if pauses, err := c.pausedRepo.PausesForTask(t.Context(), taskID); err != nil || len(pauses) != 1 {
		t.Errorf("pauses of the task = %d (%v), want 1: the answered step not asked again", len(pauses), err)
	}
	if recC.calls() != 1 || receiveOn(t, brkC, afterC) == nil {
		t.Errorf("tool calls = %d, want the approved step run once and the run continued", recC.calls())
	}
	if c.residentByID(executionID) == nil {
		t.Error("the run is not executing on the AWE the answer was redelivered to")
	}
}

func TestRun_RequestWhoseRunLookupFailsOnceIsStillDropped(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	const taskID = "task-lookup-blip"
	_, executionID := parkedTask(t, a, taskID)
	b, recB, _ := newPeer(t, a)
	if _, err := b.pausedDB.ExecContext(t.Context(), `ALTER TABLE workflow_checkpoint RENAME TO workflow_checkpoint_hidden`); err != nil {
		t.Fatalf("hide runs: %v", err)
	}
	time.AfterFunc(20*time.Millisecond, func() {
		_, _ = b.pausedDB.ExecContext(context.Background(), `ALTER TABLE workflow_checkpoint_hidden RENAME TO workflow_checkpoint`)
	})

	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)
	b.handleTaskRequest(t.Context(), *req, msg)
	b.wg.Wait()

	if executionFor(b, taskID) != nil || recB.executed() || acks.acks() != 1 {
		t.Errorf("started=%v acks=%d, want the redelivered request dropped", executionFor(b, taskID) != nil, acks.acks())
	}
	if run := runRowOf(t, b, executionID); run.Status != "parked" {
		t.Errorf("run = %s, want still parked", run.Status)
	}
}

func TestRun_EventRedeliveredAfterItsAWEDiedEndsTheRunOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		want   a2a.TaskState
		holder func(executionID, taskID string) string
		send   func(w *WorkflowInstance, executionID, taskID, id string, acks *ackCounter, redelivered bool)
	}{
		{
			name:   "deadline",
			want:   a2a.TaskStateCancelled,
			holder: func(executionID, _ string) string { return deadlineHolder(executionID, "d1") },
			send: func(w *WorkflowInstance, executionID, _, id string, acks *ackCounter, redelivered bool) {
				msg := broker.NewDeliveredMessage(id, a2a.WorkflowDeadlineTopic("test-ns", "wf", executionID), []byte(id),
					func() { acks.n.Add(1) }, func() {})
				msg.Redelivered = redelivered
				w.handleDeadline(t.Context(), msg)
			},
		},
		{
			name:   "cancel",
			want:   a2a.TaskStateCancelled,
			holder: func(_, taskID string) string { return cancelHolder(taskID, "d1") },
			send: func(w *WorkflowInstance, _, taskID, id string, acks *ackCounter, redelivered bool) {
				msg := broker.NewDeliveredMessage(id, "", nil, func() { acks.n.Add(1) }, func() {})
				msg.ReplicationGroupMessageID = id
				msg.Redelivered = redelivered
				w.handleCancelRequest(t.Context(), a2a.Request{Method: "tasks/cancel", Params: map[string]any{"id": taskID}}, msg)
			},
		},
	} {
		for flag, checkpoints := range map[string]bool{"checkpoints off": false, "checkpoints on": true} {
			t.Run(tc.name+"/"+flag, func(t *testing.T) {
				a, _ := newHILWorkflow(t, gatedCallNode())
				if checkpoints {
					a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
				}
				taskID := "task-event-redelivered-" + tc.name
				_, executionID := parkedTask(t, a, taskID)
				if _, took, err := a.runStore().TakeOverParked(t.Context(), "wf", executionID, tc.holder(executionID, taskID)); err != nil || !took {
					t.Fatalf("TakeOverParked = %v, %v", took, err)
				}

				c, recC, brkC := newPeer(t, a)
				c.checkpointRepo = a.checkpointRepo
				replies := subscribed(t, brkC, requestAckReplyTo)
				var dupAcks ackCounter
				tc.send(c, executionID, taskID, "d2", &dupAcks, true)
				c.wg.Wait()
				if dupAcks.acks() != 1 || c.residentByID(executionID) != nil {
					t.Fatalf("second message: acks=%d resident=%v, want it dropped as a duplicate", dupAcks.acks(), c.residentByID(executionID) != nil)
				}
				if run := runRowOf(t, c, executionID); run.Status != "held" || run.HeldBy != tc.holder(executionID, taskID) {
					t.Fatalf("after the second message run = %s held by %q, want still held by the first", run.Status, run.HeldBy)
				}

				var acks ackCounter
				tc.send(c, executionID, taskID, "d1", &acks, true)
				c.wg.Wait()
				c.drainRetirements()
				if recC.executed() {
					t.Error("the ended run dispatched its gated tool")
				}
				if got := finalState(t, brkC, replies); got != tc.want {
					t.Errorf("final state = %q, want %q", got, tc.want)
				}
				if again := receiveOn(t, brkC, replies); again != nil {
					t.Error("the run answered its caller twice")
				}
				if run := runRowOf(t, c, executionID); run.Status != "ended" || acks.acks() != 1 {
					t.Errorf("run = %s with %d acks, want ended and the redelivered message acknowledged", run.Status, acks.acks())
				}
			})
		}
	}
}

func TestRun_SweepSendsTheDeadlineOfAParkedRunWhoseTimerWasLost(t *testing.T) {
	a, _ := newHILWorkflow(t, gatedCallNode())
	deadlines := subscribed(t, a.svc.Broker(), a2a.WorkflowDeadlineSubscription("test-ns", "wf"))
	_, executionID := parkedTask(t, a, "task-lost-timer")
	past := time.Now().Add(-expiryBackstopMargin - time.Minute).UnixMilli()
	if _, err := a.pausedDB.ExecContext(t.Context(), `UPDATE workflow_checkpoint SET deadline_at = ? WHERE execution_id = ?`, past, executionID); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	a.sweepParkedDeadlines(t.Context())

	published := receiveOn(t, a.svc.Broker(), deadlines)
	if published == nil || published.Topic != a2a.WorkflowDeadlineTopic("test-ns", "wf", executionID) {
		t.Fatalf("deadline message = %+v, want one for the parked run past its deadline", published)
	}
}

func TestRun_EventRetakenWhileItsExitHandlerRanEndsTheRun(t *testing.T) {
	sendDeadline := func(w *WorkflowInstance, executionID, _ string, acks *ackCounter, redelivered bool) {
		msg := broker.NewDeliveredMessage("d1", a2a.WorkflowDeadlineTopic("test-ns", "wf", executionID), []byte("d1"),
			func() { acks.n.Add(1) }, func() {})
		msg.Redelivered = redelivered
		w.handleDeadline(t.Context(), msg)
	}
	sendCancel := func(w *WorkflowInstance, _, taskID string, acks *ackCounter, redelivered bool) {
		msg := broker.NewDeliveredMessage("c1", "", nil, func() { acks.n.Add(1) }, func() {})
		msg.ReplicationGroupMessageID = "c1"
		msg.Redelivered = redelivered
		w.handleCancelRequest(t.Context(), a2a.Request{Method: "tasks/cancel", Params: map[string]any{"id": taskID}}, msg)
	}
	for name, send := range map[string]func(*WorkflowInstance, string, string, *ackCounter, bool){"deadline": sendDeadline, "cancel": sendCancel} {
		t.Run(name, func(t *testing.T) {
			cleanup := &NodeConfig{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"}
			a, _ := newHILWorkflow(t, gatedCallNode(), cleanup)
			a.cfg.OnExit = &ExitHandler{Always: "cleanup"}
			a.dag.InnerNodes["cleanup"] = true
			a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
			taskID := "task-exit-retake-" + name
			_, executionID := parkedTask(t, a, taskID)

			b, _, _ := newPeer(t, a)
			b.checkpointRepo = a.checkpointRepo
			var bAcks ackCounter
			send(b, executionID, taskID, &bAcks, false)
			b.wg.Wait()
			exec := b.residentByID(executionID)
			if exec == nil {
				t.Fatal("the event did not take the parked run over")
			}
			exec.Executor.mu.Lock()
			inExit := exec.State.InExitHandler
			exec.Executor.mu.Unlock()
			if !inExit {
				t.Fatal("the exit handler is not in flight on the AWE that took the event")
			}
			deadHandler := subTaskOf(t, exec, "cleanup")
			if snap := runRowOf(t, b, executionID).StateSnapshot; !strings.Contains(snap, "in_exit_handler") {
				t.Fatalf("stored state does not show the exit handler in flight: %.300s", snap)
			}

			c, _, brkC := newPeer(t, a)
			c.checkpointRepo = a.checkpointRepo
			replies := subscribed(t, brkC, requestAckReplyTo)
			var cAcks ackCounter
			send(c, executionID, taskID, &cAcks, true)
			c.wg.Wait()
			restored := c.residentByID(executionID)
			if restored == nil {
				t.Fatal("the redelivered event did not continue the run")
			}
			handler := subTaskOf(t, restored, "cleanup")
			if handler == deadHandler {
				t.Fatal("the exit handler in flight on the dead AWE was not dispatched again")
			}
			deliverSubTaskResult(t, c, handler, a2a.TaskStateCompleted)
			c.wg.Wait()

			if got := finalState(t, brkC, replies); got != a2a.TaskStateCancelled {
				t.Errorf("final state = %q, want canceled", got)
			}
			waitForCondition(t, 2*time.Second, func() bool { return cAcks.acks() == 1 },
				"the redelivered event was not acknowledged once the run ended")
			if run := runRowOf(t, c, executionID); run.Status != "ended" {
				t.Errorf("run = %s, want ended", run.Status)
			}
		})
	}
}

func TestRun_AnswerWhoseClaimLandedButReturnedAnErrorIsAppliedOnce(t *testing.T) {
	a, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	const taskID = "task-claim-landed"
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	a.handleTaskRequest(t.Context(), *req, msg)
	a.wg.Wait()
	pause := onlyPausedRow(t, a, taskID)

	b, _, _ := newPeer(t, a)
	var bAcks ackCounter
	b.handleWorkflowHILResponse(t.Context(), answerFromQueue(b, pause.RequestID, true, &bAcks, false))
	if pauseStatus(t, a, pause.RequestID) != "answered" {
		t.Fatalf("pause %s, want the answer written and not applied", pauseStatus(t, a, pause.RequestID))
	}

	hide := hidePausesOnce(t, a, 300*time.Millisecond)
	a.resumeClaimHook = func() {
		run := runRowOf(t, a, pause.ExecutionID)
		if _, claimed, err := a.runStore().ClaimHeld(t.Context(), pause.RequestID, run.Holder(), "resumed"); err != nil || !claimed {
			t.Errorf("landing the claim: claimed=%v err=%v", claimed, err)
		}
		hide()
	}
	deliverSubTaskResult(t, a, subTaskOf(t, executionFor(a, taskID), "other"), a2a.TaskStateCompleted)

	waitForCondition(t, 6*time.Second, rec.executed, "the answer whose claim landed was never applied")
	waitForCondition(t, 3*time.Second, func() bool { return acks.acks() == 1 }, "the run never ended")
	if rec.calls() != 1 {
		t.Errorf("approved tool ran %d times, want 1", rec.calls())
	}
}
