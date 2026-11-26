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
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const (
	hilStopReplyTo   = "test/reply/hil-stop"
	hilStopRequestID = "req-paused"
	hilStopQuietWait = 300 * time.Millisecond
)

type hilStopFixture struct {
	w       *WorkflowInstance
	exec    *WorkflowExecution
	brk     broker.Broker
	dbPath  string
	replies broker.Queue
}

func newHILStopFixture(t *testing.T, withOnExit bool) *hilStopFixture {
	t.Helper()
	w, brk := newHILInstance(t)

	dbPath := filepath.Join(t.TempDir(), "hil-stop.db")
	db, err := wfstore.Open("sqlite:///"+dbPath, slog.Default())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	w.pausedDB = db
	w.pausedRepo = wfstore.NewPausedRepo(db)

	replies, err := brk.CreateQueue("hil-stop-replies", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create reply queue: %v", err)
	}
	if err = brk.Subscribe(replies, hilStopReplyTo); err != nil {
		t.Fatalf("subscribe reply queue: %v", err)
	}

	w.cfg.Nodes = []*NodeConfig{
		{ID: "approve", Type: "agent", AgentName: "approver"},
		{ID: "next", Type: "agent", AgentName: "nextAgent", DependsOn: []string{"approve"}},
	}
	if withOnExit {
		w.cfg.Nodes = append(w.cfg.Nodes, &NodeConfig{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"})
		w.cfg.OnExit = &ExitHandler{Always: "cleanup"}
	}
	dag, err := BuildDependencyGraph(w.cfg.Nodes)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}
	if withOnExit {
		dag.InnerNodes["cleanup"] = true
	}
	w.dag = dag
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(string, Event) {})
	go w.timerManager.Start(w.ctx)

	state := NewExecutionState("wf", "exec-paused")
	state.AddPending("approve")
	execCtx := NewExecutionContext(w.ctx, "task-paused", "ctx-1", nil,
		a2a.UserProperties{UserID: "alice", ReplyTo: hilStopReplyTo}, false)
	e := &DAGExecutor{cfg: w.cfg, dag: w.dag, instance: w, state: state, execCtx: execCtx, logger: slog.Default()}
	exec := w.registerExecution(state.ExecutionID, &WorkflowExecution{State: state, Context: execCtx, Executor: e})

	e.mu.Lock()
	err = w.pauseWorkflowNode(t.Context(), e, &EventNodePaused{
		NodeID: "approve", SubTaskID: "st-1", RequestID: hilStopRequestID,
		Surface:   json.RawMessage(`{"surfaceId":"s"}`),
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	e.mu.Unlock()
	if err != nil {
		t.Fatalf("pauseWorkflowNode: %v", err)
	}
	return &hilStopFixture{w: w, exec: exec, brk: brk, dbPath: dbPath, replies: replies}
}

func (f *hilStopFixture) park(t *testing.T) {
	t.Helper()
	e := f.exec.Executor
	e.mu.Lock()
	parked := e.writeParkLocked()
	e.mu.Unlock()
	if !parked {
		t.Fatal("the paused run did not park")
	}
	f.w.evictParked(e)
}

func (f *hilStopFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func (f *hilStopFixture) terminalResponses(t *testing.T, wait time.Duration) []a2a.Task {
	t.Helper()
	var tasks []a2a.Task
	for {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		msg, err := f.brk.Receive(ctx, f.replies)
		cancel()
		if err != nil {
			return tasks
		}
		var resp struct {
			Result *a2a.Task `json:"result"`
		}
		if json.Unmarshal(msg.Payload, &resp) == nil && resp.Result != nil && resp.Result.Kind == "task" {
			tasks = append(tasks, *resp.Result)
		}
		wait = hilStopQuietWait
	}
}

func (f *hilStopFixture) requireOneTerminal(t *testing.T, want a2a.TaskState) a2a.Task {
	t.Helper()
	tasks := f.terminalResponses(t, 2*time.Second)
	if len(tasks) != 1 {
		t.Fatalf("caller received %d terminal responses, want exactly 1", len(tasks))
	}
	if tasks[0].Status.State != want {
		t.Errorf("terminal state = %q, want %q", tasks[0].Status.State, want)
	}
	return tasks[0]
}

func (f *hilStopFixture) requireNoTerminal(t *testing.T) {
	t.Helper()
	if tasks := f.terminalResponses(t, hilStopQuietWait); len(tasks) != 0 {
		t.Fatalf("caller received %d terminal responses, want none", len(tasks))
	}
}

func (f *hilStopFixture) rowStatusAfterStop(t *testing.T) string {
	t.Helper()
	db, err := wfstore.Open("sqlite:///"+f.dbPath, slog.Default())
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = db.Close() }()
	row, err := wfstore.NewPausedRepo(db).Get(context.Background(), hilStopRequestID)
	if err != nil || row == nil {
		t.Fatalf("read paused row: row=%v err=%v", row, err)
	}
	return row.Status
}

func finalized(exec *WorkflowExecution) bool {
	exec.Executor.mu.Lock()
	defer exec.Executor.mu.Unlock()
	return exec.State.Finalized
}

func requireStoppedError(t *testing.T, task a2a.Task) {
	t.Helper()
	if got, _ := task.Metadata["error"].(string); !strings.Contains(got, workflowInstanceStoppedMsg) {
		t.Errorf("terminal error = %q, want the stopped-instance message", got)
	}
}

func TestStop_LeavesPauseParkedHere(t *testing.T) {
	f := newHILStopFixture(t, false)

	f.stop(t)

	if finalized(f.exec) {
		t.Fatal("HIL-paused execution was finalized on stop; its pause could no longer be resumed")
	}
	f.requireNoTerminal(t)
	if got := f.rowStatusAfterStop(t); got != "paused" {
		t.Fatalf("paused row status after stop = %q, want it left paused for resume", got)
	}
}

func TestStop_LeavesPauseResolvedElsewhere(t *testing.T) {
	f := newHILStopFixture(t, false)
	if _, claimed, err := f.w.pausedRepo.Claim(t.Context(), hilStopRequestID, "resumed"); err != nil || !claimed {
		t.Fatalf("claim as another pod: claimed=%v err=%v", claimed, err)
	}

	f.stop(t)

	if finalized(f.exec) {
		t.Fatal("execution resumed by another pod was finalized here")
	}
	f.requireNoTerminal(t)
}

func TestStop_FailsPauseResolvedHere(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.w.answer(t.Context(), hilStopRequestID, "resumed", "approved", nil)
	if finalized(f.exec) {
		t.Fatal("run finished on resume; test setup expects it to keep running")
	}

	f.stop(t)

	requireStoppedError(t, f.requireOneTerminal(t, a2a.TaskStateFailed))
}

func TestFailInFlightExecutions_ResumedHereIgnoresUnreadableStore(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.w.answer(t.Context(), hilStopRequestID, "resumed", "approved", nil)
	if err := f.w.pausedDB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	f.w.failInFlightExecutions()

	if !finalized(f.exec) {
		t.Fatal("run resumed on this pod was left unfinalized because the store was unreadable")
	}
	requireStoppedError(t, f.requireOneTerminal(t, a2a.TaskStateFailed))
}

func TestStop_FailsReconstructedRunResumedHere(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.park(t)
	f.w.answer(t.Context(), hilStopRequestID, "resumed", "approved", nil)

	f.stop(t)

	requireStoppedError(t, f.requireOneTerminal(t, a2a.TaskStateFailed))
}

func TestFailInFlightExecutions_ReconstructedRunConsultsStore(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.park(t)
	rebuilt, err := f.w.rebuildFromRun(runRowOf(t, f.w, "exec-paused"), nil, "")
	if err != nil {
		t.Fatalf("rebuildFromRun: %v", err)
	}
	t.Cleanup(rebuilt.Context.Cancel)
	f.w.registerExecution(rebuilt.State.ExecutionID, rebuilt)

	f.w.failInFlightExecutions()
	if finalized(rebuilt) {
		t.Fatal("reconstructed run with a still-paused row was finalized on stop")
	}

	if err = f.w.pausedDB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	f.w.failInFlightExecutions()
	if finalized(rebuilt) {
		t.Fatal("reconstructed run was finalized when the paused-row lookup failed; its rows would be retired")
	}
	f.requireNoTerminal(t)
}

func TestStop_FinalizesDecidedRunDespiteParkedPause(t *testing.T) {
	for name, event := range map[string]Event{
		"cancel":           &EventCancel{Reason: "client-requested"},
		"workflow_timeout": &EventWorkflowTimeout{},
	} {
		t.Run(name, func(t *testing.T) {
			f := newHILStopFixture(t, true)
			if err := f.exec.Executor.Advance(t.Context(), event); err != nil {
				t.Fatalf("advance: %v", err)
			}
			f.exec.Executor.mu.Lock()
			inExitChain := f.exec.State.InExitHandler
			f.exec.Executor.mu.Unlock()
			if !inExitChain || finalized(f.exec) {
				t.Fatalf("test setup: want the run waiting in its exit chain, got inExitHandler=%v finalized=%v", inExitChain, finalized(f.exec))
			}

			f.stop(t)

			f.requireOneTerminal(t, a2a.TaskStateCancelled)
			if got := f.rowStatusAfterStop(t); got == "paused" {
				t.Error("the decided run's pause is still answerable after stop; it should have been retired")
			}
		})
	}
}

func TestAnswer_LeftOnThePauseWhileStopping(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.w.failInFlightExecutions()

	f.w.answer(t.Context(), hilStopRequestID, "resumed", "approved", nil)

	row, err := f.w.pausedRepo.Get(t.Context(), hilStopRequestID)
	if err != nil || row == nil || row.Status != "answered" {
		t.Fatalf("answer during stop: row=%+v err=%v, want it written on the pause and not applied", row, err)
	}
	f.exec.Executor.mu.Lock()
	stillPending := f.exec.State.PendingNodes["approve"]
	f.exec.Executor.mu.Unlock()
	if !stillPending {
		t.Error("answer during stop advanced the paused node")
	}
}

func TestAnswer_StopLandingDuringTheTakeOverLeavesTheRunForTheNextHolder(t *testing.T) {
	f := newHILStopFixture(t, false)
	f.park(t)
	f.w.resumeClaimHook = f.w.failInFlightExecutions

	f.w.answer(t.Context(), hilStopRequestID, "resumed", "approved", nil)

	f.requireNoTerminal(t)
	f.w.executionsMu.Lock()
	registered := len(f.w.executions)
	f.w.executionsMu.Unlock()
	if registered != 0 {
		t.Errorf("%d executions registered on a stopping instance, want 0", registered)
	}
	if run := runRowOf(t, f.w, "exec-paused"); run.Status != "held" || run.HeldBy != hilStopRequestID {
		t.Errorf("run = %s held by %q, want it held by the answer for its redelivery", run.Status, run.HeldBy)
	}
}

func TestAnswer_ApprovalDuringStopIsNotDispatched(t *testing.T) {
	e, rec := newLivePauseExecutor(t)
	requestID := pauseAndGetRequestID(t, e, map[string]any{"url": "https://example.test"})
	w := e.instance
	w.resumeClaimHook = w.failInFlightExecutions

	w.answer(t.Context(), requestID, "resumed", "answered", map[string]any{"approved": true})
	w.wg.Wait()

	if rec.executed() {
		t.Fatal("an approval taken while the instance was stopping dispatched the tool")
	}
	if run := runRowOf(t, w, "exec-live"); run.Status != "held" || run.HeldBy != requestID {
		t.Errorf("run = %s held by %q, want it held by the answer for its redelivery", run.Status, run.HeldBy)
	}
}
