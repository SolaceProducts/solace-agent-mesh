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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func checkpointFlag() *features.Registry {
	return flagsOn(features.KeyWorkflowCheckpointResume)
}

func assertSubTaskSuperseded(t *testing.T, a *checkpointAWE, exec *WorkflowExecution, subTaskID, agentName string) {
	t.Helper()
	if !cancelSentFor(a.brk.snapshot(), a2a.AgentRequestTopic("test-ns", agentName), subTaskID) {
		t.Errorf("restored run did not cancel the dead attempt's sub-task at %q", agentName)
	}
	exec.Executor.mu.Lock()
	defer exec.Executor.mu.Unlock()
	if node, ok := exec.State.NodeForSubTask(subTaskID); ok {
		t.Errorf("restored run still routes the dead attempt's sub-task to %q", node)
	}
	for _, timer := range exec.State.Timers {
		if timer.SubTaskID == subTaskID {
			t.Errorf("restored run keeps the dead attempt's %s timer", timer.Kind)
		}
	}
}

func cancelSentFor(msgs []capturedMessage, topic, subTaskID string) bool {
	for _, m := range msgs {
		if m.Topic != topic || !strings.Contains(string(m.Payload), `"tasks/cancel"`) {
			continue
		}
		var req a2a.Request
		if json.Unmarshal(m.Payload, &req) == nil && req.ID == subTaskID {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCheckpointResume_MapRelaunchesOnlyItsUnfinishedBranch(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "fanout",
			Namespace: "test-ns",
			Nodes: []*NodeConfig{
				{ID: "fanout", Type: "map", ItemsExpression: []any{"a", "b", "c"}, TemplateNode: "item", ConcurrencyLimit: 1},
				{ID: "item", Type: "agent", AgentName: "itemWorker", Timeout: time.Hour},
			},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0101"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "itemWorker", "result-a")
	if got := first.sendsTo("itemWorker"); got != 2 {
		t.Fatalf("first process launched %d branches, want 2 (one done, one in flight)", got)
	}
	deadBranch := lastRequestIDSentTo(t, first.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "itemWorker"))
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	if got := second.sendsTo("itemWorker"); got != 1 {
		t.Fatalf("restored map launched %d branches, want only the one in flight", got)
	}
	restored := executionFor(second.w, taskID)
	assertSubTaskSuperseded(t, second, restored, deadBranch, "itemWorker")
	restored.Executor.mu.Lock()
	branches := slices.Clone(restored.State.ActiveBranches["fanout"])
	restored.Executor.mu.Unlock()
	if len(branches) != 2 || branches[0].Index != 0 || branches[1].Index != 1 {
		t.Fatalf("restored map branches = %+v, want one per launched item (0 and 1)", branches)
	}
	if _, attempt := lastRetryIdentitySentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "itemWorker")); attempt != 1 {
		t.Errorf("relaunched branch retry attempt = %d, want 1", attempt)
	}
	second.answer(t, "itemWorker", "result-b")
	if got := second.sendsTo("itemWorker"); got != 2 {
		t.Fatalf("restored map launched %d branches after the relaunched one finished, want the last item next", got)
	}
	second.answer(t, "itemWorker", "result-c")

	final := finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo)
	if final == nil || final.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("restored map run final response = %+v, want completed", final)
	}
	exec := executionFor(second.w, taskID)
	if exec != nil {
		t.Fatalf("finished run still registered")
	}
	if secondAcks.acks() != 1 {
		t.Errorf("request acknowledged %d times, want 1", secondAcks.acks())
	}
}

func TestCheckpointResume_LoopContinuesWithTheIterationInFlight(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "looping",
			Namespace: "test-ns",
			Nodes: []*NodeConfig{
				{ID: "repeat", Type: "loop", BodyNode: "body", Condition: "1 == 1", MaxIterations: 3},
				{ID: "body", Type: "agent", AgentName: "loopWorker", Timeout: time.Hour},
			},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0201"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "loopWorker", "iteration-0")
	deadIteration := lastRequestIDSentTo(t, first.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "loopWorker"))
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	if got := second.sendsTo("loopWorker"); got != 1 {
		t.Fatalf("restored loop dispatched %d iterations, want only the one in flight", got)
	}
	restored := executionFor(second.w, taskID)
	assertSubTaskSuperseded(t, second, restored, deadIteration, "loopWorker")
	restored.Executor.mu.Lock()
	iterations := len(restored.State.ActiveBranches["repeat"])
	restored.Executor.mu.Unlock()
	if iterations != 2 {
		t.Errorf("restored loop records %d iterations, want 2 (the finished one and the relaunched one)", iterations)
	}
	if _, attempt := lastRetryIdentitySentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "loopWorker")); attempt != 1 {
		t.Errorf("relaunched iteration retry attempt = %d, want 1", attempt)
	}
	second.answer(t, "loopWorker", "iteration-1")
	second.answer(t, "loopWorker", "iteration-2")

	if got := first.sendsTo("loopWorker") + second.sendsTo("loopWorker"); got != 4 {
		t.Errorf("iterations dispatched across both processes = %d, want 4 (three plus the one in flight again)", got)
	}
	if finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) == nil {
		t.Fatal("restored loop run did not answer its caller")
	}
}

func TestCheckpointResume_RetryBackoffKeepsItsTimerAndCount(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "retrying",
			Namespace: "test-ns",
			Nodes: []*NodeConfig{{
				ID: "flaky", Type: "agent", AgentName: "flakyAgent",
				Retry: &RetryStrategy{Limit: 2, Backoff: &BackoffStrategy{Duration: 2 * time.Second, Factor: 1}},
			}},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0301"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	subTaskID := lastRequestIDSentTo(t, first.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "flakyAgent"))
	deliverSubTaskText(t, first.w, subTaskID, a2a.TaskStateFailed, "transient failure")
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	if got := second.sendsTo("flakyAgent"); got != 0 {
		t.Fatalf("node waiting out its backoff dispatched %d times at the restore, want 0 until the timer fires", got)
	}
	waitFor(t, "the retry to fire", func() bool { return second.sendsTo("flakyAgent") == 1 })
	exec := executionFor(second.w, taskID)
	exec.Executor.mu.Lock()
	retries := exec.State.RetryCounts["flaky"]
	exec.Executor.mu.Unlock()
	if retries != 1 {
		t.Errorf("retry count after the restored retry = %d, want 1", retries)
	}
	second.answer(t, "flakyAgent", "worked")
	if finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) == nil {
		t.Fatal("restored run did not answer its caller")
	}
}

func TestCheckpointResume_ParkedTimerKeepsItsOriginalFiringTime(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:          "timing",
			Namespace:     "test-ns",
			OutputMapping: map[string]any{"done": "yes"},
			Nodes: []*NodeConfig{
				{ID: "cool", Type: NodeTypeTimer, WaitDuration: time.Hour},
				{ID: "work", Type: "agent", AgentName: "workAgent"},
			},
		}
	}
	svc := func() *stubServices {
		return &stubServices{
			brk:      &capturingBroker{},
			features: flagsOn(features.KeyWorkflowCheckpointResume, features.KeyWorkflowDurableWaits, hilPauseResumeFlag),
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWEWith(t, cfg(), dbURL, svc())
	const taskID = "019e0000-0000-7000-8000-00000c0e0701"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	wantFiresAt := wakeTimerFiresAt(t, executionFor(first.w, taskID), "cool")
	first.kill()

	second := startCheckpointAWEWith(t, cfg(), dbURL, svc())
	second.deliver(t, taskID, &secondAcks, true)

	if got := second.sendsTo("workAgent"); got != 1 {
		t.Fatalf("node in flight at the death dispatched %d times at the restore, want 1", got)
	}
	if got := wakeTimerFiresAt(t, executionFor(second.w, taskID), "cool"); !got.Equal(wantFiresAt) {
		t.Errorf("restored timer fires at %s, want its original %s", got, wantFiresAt)
	}
}

func wakeTimerFiresAt(t *testing.T, exec *WorkflowExecution, nodeID string) time.Time {
	t.Helper()
	if exec == nil {
		t.Fatal("the run is not executing")
	}
	exec.Executor.mu.Lock()
	defer exec.Executor.mu.Unlock()
	for _, timer := range exec.State.Timers {
		if timer.Kind == TimerKindWake && timer.NodeID == nodeID {
			return timer.FiresAt
		}
	}
	t.Fatalf("run has no wake timer for node %q", nodeID)
	return time.Time{}
}

func TestCheckpointResume_ExitHandlersRunOnce(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "with-exit",
			Namespace: "test-ns",
			OnExit:    &ExitHandler{OnSuccess: "onSuccess", Always: "always"},
			Nodes: []*NodeConfig{
				{ID: "main", Type: "agent", AgentName: "mainAgent"},
				{ID: "onSuccess", Type: "agent", AgentName: "successHandler"},
				{ID: "always", Type: "agent", AgentName: "alwaysHandler"},
			},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0401"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "mainAgent", "main-done")
	firstHandler, secondHandler := "successHandler", "alwaysHandler"
	if first.sendsTo(firstHandler) == 0 {
		firstHandler, secondHandler = secondHandler, firstHandler
	}
	first.answer(t, firstHandler, "handled")
	if first.sendsTo(secondHandler) != 1 {
		t.Fatalf("second exit handler not in flight before the kill")
	}
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	if got := second.sendsTo("mainAgent") + second.sendsTo(firstHandler); got != 0 {
		t.Fatalf("finished node or exit handler dispatched %d times after the restore, want 0", got)
	}
	if got := second.sendsTo(secondHandler); got != 1 {
		t.Fatalf("exit handler in flight dispatched %d times after the restore, want 1", got)
	}
	second.answer(t, secondHandler, "handled")
	final := finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo)
	if final == nil || final.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("final response = %+v, want completed", final)
	}
}

func TestCheckpointResume_DeadlineIsNotExtended(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:            "deadline",
			Namespace:       "test-ns",
			WorkflowTimeout: 600 * time.Millisecond,
			Nodes:           []*NodeConfig{{ID: "slow", Type: "agent", AgentName: "slowAgent"}},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0501"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	exec := executionFor(first.w, taskID)
	exec.Executor.mu.Lock()
	deadline := exec.State.WorkflowDeadline
	exec.Executor.mu.Unlock()
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	restored := executionFor(second.w, taskID)
	restored.Executor.mu.Lock()
	restoredDeadline := restored.State.WorkflowDeadline
	restored.Executor.mu.Unlock()
	if !restoredDeadline.Equal(deadline) {
		t.Fatalf("restored deadline = %v, want the original %v", restoredDeadline, deadline)
	}
	waitFor(t, "the run to time out", func() bool {
		return finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) != nil
	})
	if final := finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo); final.Status.State == a2a.TaskStateCompleted {
		t.Errorf("final state = %q, want the run ended by its deadline", final.Status.State)
	}
}

func TestCheckpointResume_FlagOffWritesNothingAndStartsOver(t *testing.T) {
	dbURL := sharedStoreURL(t)
	hilOnly := flagsOn("workflow_hil_pause_resume")
	first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, hilOnly)
	const taskID = "019e0000-0000-7000-8000-00000c0e0601"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "agent1", "output-of-n1")
	if first.w.checkpointStore() != nil {
		t.Fatal("checkpoint store opened with the flag off")
	}
	db, err := wfstore.Open(dbURL, slog.Default())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if row, err := latestRun(t.Context(), db, taskID); err != nil || row != nil {
		t.Fatalf("checkpoint with the flag off = %+v (err %v), want none", row, err)
	}
	first.kill()

	second := startCheckpointAWE(t, fourStepWorkflow(), dbURL, hilOnly)
	second.deliver(t, taskID, &secondAcks, true)
	if got := second.sendsTo("agent1"); got != 1 {
		t.Errorf("with the flag off a redelivery dispatched n1 %d times, want 1: the run starts over", got)
	}
}

func TestCheckpointResume_RunThatAlreadyAnsweredIsDropped(t *testing.T) {
	dbURL := sharedStoreURL(t)
	awe := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0701"
	state := NewExecutionState("four-steps", "0190a1b2-0000-7000-8000-00000000c0de")
	snapshot, _ := json.Marshal(state)
	execContext, _ := json.Marshal(persistedExecContext{TaskID: taskID, UserProps: a2a.UserProperties{UserID: "alice", ReplyTo: checkpointReplyTo}})
	putCheckpoint(t, awe.w.checkpointStore(), testCheckpoint{
		TaskID: taskID, ExecutionID: state.ExecutionID, WorkflowName: "four-steps", DefinitionHash: awe.w.definitionHash,
		StateSnapshot: string(snapshot), ExecContext: string(execContext),
	})
	if err := markAnswered(t, awe.w.checkpointStore(), state.ExecutionID); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	var acks ackCounter
	awe.deliver(t, taskID, &acks, true)
	if acks.acks() != 1 {
		t.Errorf("redelivery of an answered run acknowledged %d times, want 1", acks.acks())
	}
	if finalTaskOn(t, awe.brk.snapshot(), checkpointReplyTo) != nil {
		t.Error("an answered run answered its caller again")
	}
	if got := awe.sendsTo("agent1"); got != 0 {
		t.Errorf("an answered run dispatched n1 %d times, want 0", got)
	}
	if row := checkpointOf(t, awe, taskID); !runAnswered(row) {
		t.Error("the answered run's row lost its answered mark")
	}
}

func predatePause(t *testing.T, w *WorkflowInstance, taskID string) {
	t.Helper()
	row, err := w.runOfTask(t.Context(), taskID)
	if err != nil || row == nil {
		t.Fatalf("read stored run: %v, %v", row, err)
	}
	if _, err := w.pausedDB.ExecContext(t.Context(),
		`UPDATE workflow_checkpoint SET open_pauses = '[]' WHERE execution_id = ?`, row.ExecutionID); err != nil {
		t.Fatalf("rewrite stored run: %v", err)
	}
}

func TestCheckpointResume_PauseOpenedAfterTheLastWriteIsAskedAgain(t *testing.T) {
	first, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
	firstCtx, killFirst := context.WithCancel(t.Context())
	first.ctx = firstCtx
	const taskID = "task-checkpoint-predates-pause"
	var firstAcks, secondAcks ackCounter
	req, msg := deliveredRequest(taskID, &firstAcks, false)
	first.handleTaskRequest(t.Context(), *req, msg)
	first.wg.Wait()
	original := onlyPausedRow(t, first, taskID)
	predatePause(t, first, taskID)
	first.processStopping.Store(true)
	killFirst()

	second, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
	second.checkpointRepo = first.checkpointRepo
	req, msg = deliveredRequest(taskID, &secondAcks, true)
	second.handleTaskRequest(t.Context(), *req, msg)
	second.wg.Wait()

	if rec.executed() {
		t.Error("the restore ran the tool still waiting for approval")
	}
	rows, err := first.pausedRepo.PausesForTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("PausesForTask: %v", err)
	}
	var closed, open int
	for _, p := range rows {
		switch {
		case p.RequestID == original.RequestID && p.Status == statusClosedForRestart:
			closed++
		case p.RequestID != original.RequestID && p.Status == "paused":
			open++
		}
	}
	if closed != 1 || open != 1 {
		t.Errorf("pauses after the restore = %+v, want the original closed and one new pause open", rows)
	}
}

func TestCheckpointResume_WaitNodeInFlightWhenTheAWEDiedOpensItsSignalOnceAndASignalCompletesIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		taskID  string
		timeout time.Duration
	}{
		{name: "a wait node without a timeout", taskID: "019e0000-0000-7000-8000-00000c0e0801"},
		{name: "a wait node with a timeout", taskID: "019e0000-0000-7000-8000-00000c0e0802", timeout: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := func() *WorkflowConfig {
				return &WorkflowConfig{
					Name:          "waiting",
					Namespace:     "test-ns",
					OutputMapping: map[string]any{"done": "yes"},
					Nodes: []*NodeConfig{
						waitNode(tc.timeout),
						{ID: "work", Type: "agent", AgentName: "workAgent"},
					},
				}
			}
			svc := func() *stubServices {
				return &stubServices{
					brk:      &capturingBroker{},
					features: flagsOn(features.KeyWorkflowCheckpointResume, features.KeyWorkflowDurableWaits, hilPauseResumeFlag),
				}
			}
			requestID := "signal:" + tc.taskID + ":paid"
			dbURL := sharedStoreURL(t)
			first := startCheckpointAWEWith(t, cfg(), dbURL, svc())
			var firstAcks, secondAcks ackCounter
			first.deliver(t, tc.taskID, &firstAcks, false)
			predatePause(t, first.w, tc.taskID)
			first.kill()

			second := startCheckpointAWEWith(t, cfg(), dbURL, svc())
			second.deliver(t, tc.taskID, &secondAcks, true)

			pauses, err := second.w.pausedRepo.PausesForTask(t.Context(), tc.taskID)
			if err != nil {
				t.Fatalf("PausesForTask: %v", err)
			}
			var got []pausePair
			for _, p := range pauses {
				got = append(got, pausePair{RequestID: p.RequestID, Status: p.Status})
			}
			if diff := cmp.Diff([]pausePair{{RequestID: requestID, Status: wfstore.StatusPaused}}, got); diff != "" {
				t.Fatalf("pauses after the restore mismatch (-want +got):\n%s", diff)
			}

			second.answer(t, "workAgent", "worked")
			second.w.answer(t.Context(), requestID, "", "received", nil)

			waitFor(t, "the restored run to answer its caller", func() bool { return finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) != nil })
			if state := finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo).Status.State; state != a2a.TaskStateCompleted {
				t.Errorf("final state after the signal = %s, want completed", state)
			}
		})
	}
}

func TestCheckpointResume_RunWithAStepPausedOnAPerson(t *testing.T) {
	first, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
	firstCtx, killFirst := context.WithCancel(t.Context())
	first.ctx = firstCtx
	const taskID = "task-checkpoint-paused"
	var firstAcks, secondAcks ackCounter
	req, msg := deliveredRequest(taskID, &firstAcks, false)
	first.handleTaskRequest(t.Context(), *req, msg)
	first.wg.Wait()
	pause := onlyPausedRow(t, first, taskID)
	otherFirst := subTaskOf(t, executionFor(first, taskID), "other")
	first.processStopping.Store(true)
	killFirst()

	second, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
	second.checkpointRepo = first.checkpointRepo
	req, msg = deliveredRequest(taskID, &secondAcks, true)
	second.handleTaskRequest(t.Context(), *req, msg)

	if got := onlyPausedRow(t, second, taskID); got.Status != "paused" {
		t.Fatalf("paused row after the restore = %q, want it left paused for the person's answer", got.Status)
	}
	exec := executionFor(second, taskID)
	if exec == nil || exec.State.ExecutionID != pause.ExecutionID {
		t.Fatalf("restored run is not the paused execution")
	}
	otherSecond := subTaskOf(t, exec, "other")
	if otherSecond == otherFirst {
		t.Fatal("the step in flight at the kill was not dispatched again")
	}
	second.wg.Wait()
	if rec.executed() {
		t.Fatal("the restore ran the tool still waiting for approval")
	}
	deliverSubTaskResult(t, second, otherSecond, a2a.TaskStateCompleted)
	if secondAcks.acks() != 1 {
		t.Fatalf("restored run parked on the person's answer acknowledged %d times, want 1", secondAcks.acks())
	}

	second.answer(t.Context(), pause.RequestID, "resumed", "answered", map[string]any{"approved": true})
	second.wg.Wait()
	if !rec.executed() {
		t.Fatal("the approved tool did not run on the restored run")
	}
	if row, err := second.runOfTask(t.Context(), taskID); err != nil || !runAnswered(row) {
		t.Errorf("restored run after the answer = %+v (err %v), want it finished and answered", row, err)
	}
}

func TestCheckpointResume_StopLeavesARestoredRunParkedOnAPerson(t *testing.T) {
	first, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
	firstCtx, killFirst := context.WithCancel(t.Context())
	first.ctx = firstCtx
	const taskID = "task-checkpoint-parked-stop"
	var firstAcks, secondAcks ackCounter
	req, msg := deliveredRequest(taskID, &firstAcks, false)
	first.handleTaskRequest(t.Context(), *req, msg)
	first.wg.Wait()
	first.processStopping.Store(true)
	killFirst()

	second, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
	second.checkpointRepo = first.checkpointRepo
	req, msg = deliveredRequest(taskID, &secondAcks, true)
	second.handleTaskRequest(t.Context(), *req, msg)
	exec := executionFor(second, taskID)
	if exec == nil {
		t.Fatal("the run was not restored")
	}
	deliverSubTaskResult(t, second, subTaskOf(t, exec, "other"), a2a.TaskStateCompleted)

	second.failInFlightExecutions()

	if finalized(exec) {
		t.Error("Stop failed a restored run still waiting on a person; its approval can no longer resume it")
	}
	if got := onlyPausedRow(t, second, taskID); got.Status != "paused" {
		t.Errorf("paused row after Stop = %q, want it left paused for any pod to resume", got.Status)
	}
}

func TestCheckpointResume_ParkedIterationsStayParked(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*NodeConfig
	}{
		{name: "map branches waiting for approval", nodes: []*NodeConfig{
			{ID: "fanout", Type: "map", ItemsExpression: []any{"x", "y"}, TemplateNode: "call_gated"}, gatedCallNode(), otherAgentNode(),
		}},
		{name: "a loop iteration waiting for approval", nodes: []*NodeConfig{
			{ID: "repeat", Type: "loop", BodyNode: "call_gated", Condition: "1 == 1", MaxIterations: 2}, gatedCallNode(), otherAgentNode(),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first, _ := newHILWorkflow(t, tc.nodes...)
			first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
			firstCtx, killFirst := context.WithCancel(t.Context())
			first.ctx = firstCtx
			const taskID = "task-checkpoint-parked-iterations"
			var firstAcks, secondAcks ackCounter
			req, msg := deliveredRequest(taskID, &firstAcks, false)
			first.handleTaskRequest(t.Context(), *req, msg)
			first.wg.Wait()
			before, err := first.pausedRepo.PausesForTask(t.Context(), taskID)
			if err != nil || len(before) == 0 {
				t.Fatalf("paused rows before the kill = %v (err %v), want at least one", before, err)
			}
			first.processStopping.Store(true)
			killFirst()

			second, rec := newHILWorkflow(t, tc.nodes...)
			second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
			second.checkpointRepo = first.checkpointRepo
			req, msg = deliveredRequest(taskID, &secondAcks, true)
			second.handleTaskRequest(t.Context(), *req, msg)
			second.wg.Wait()

			after, err := second.pausedRepo.PausesForTask(t.Context(), taskID)
			if err != nil || len(after) != len(before) {
				t.Errorf("paused rows after the restore = %d (err %v), want the %d already waiting", len(after), err, len(before))
			}
			if rec.executed() {
				t.Error("the restore ran a tool still waiting for approval")
			}
		})
	}
}

func TestCheckpointResume_PausedRunResumedElsewhereDropsTheRedelivery(t *testing.T) {
	first, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
	firstCtx, killFirst := context.WithCancel(t.Context())
	first.ctx = firstCtx
	const taskID = "task-checkpoint-carried"
	var firstAcks, secondAcks ackCounter
	req, msg := deliveredRequest(taskID, &firstAcks, false)
	first.handleTaskRequest(t.Context(), *req, msg)
	first.wg.Wait()
	pause := onlyPausedRow(t, first, taskID)
	first.processStopping.Store(true)
	killFirst()
	if _, err := first.pausedDB.ExecContext(t.Context(),
		`UPDATE workflow_checkpoint SET held_by = ? WHERE execution_id = ?`, pause.RequestID, pause.ExecutionID); err != nil {
		t.Fatalf("hold the run by the answer: %v", err)
	}

	second, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
	second.checkpointRepo = first.checkpointRepo
	req, msg = deliveredRequest(taskID, &secondAcks, true)
	second.handleTaskRequest(t.Context(), *req, msg)
	second.wg.Wait()

	if executionFor(second, taskID) != nil {
		t.Fatal("a second executor took a run another process carries from its paused row")
	}
	if secondAcks.acks() != 1 {
		t.Errorf("redelivery of a run carried elsewhere acknowledged %d times, want 1", secondAcks.acks())
	}
	if rec.executed() {
		t.Error("the redelivery ran the gated tool")
	}
	if row, err := first.runOfTask(t.Context(), taskID); err != nil || row == nil || row.HeldBy != pause.RequestID {
		t.Errorf("stored run after the dropped redelivery = %v (err %v), want it kept for the process carrying the run", row, err)
	}
}

func answeredWhilePaused(t *testing.T, taskID string) (*WorkflowInstance, wfstore.TaskPause) {
	t.Helper()
	first, _ := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
	firstCtx, killFirst := context.WithCancel(t.Context())
	first.ctx = firstCtx
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	first.handleTaskRequest(t.Context(), *req, msg)
	first.wg.Wait()
	pause := onlyPausedRow(t, first, taskID)
	if err := markAnswered(t, first.checkpointRepo, pause.ExecutionID); err != nil {
		t.Fatalf("mark the run answered: %v", err)
	}
	first.processStopping.Store(true)
	killFirst()
	return first, pause
}

func restartedHILWorkflow(t *testing.T, first *WorkflowInstance) (*WorkflowInstance, *approvalArgTool) {
	t.Helper()
	second, rec := newHILWorkflow(t, gatedCallNode(), otherAgentNode())
	second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
	second.checkpointRepo = first.checkpointRepo
	return second, rec
}

func TestCheckpointResume_AnsweredRunRedeliveryRetiresItsOpenPause(t *testing.T) {
	const taskID = "task-checkpoint-answered-pause"
	first, pause := answeredWhilePaused(t, taskID)
	second, rec := restartedHILWorkflow(t, first)

	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)
	second.handleTaskRequest(t.Context(), *req, msg)
	second.wg.Wait()

	if executionFor(second, taskID) != nil {
		t.Fatal("redelivery of an answered run restored it")
	}
	if acks.acks() != 1 {
		t.Errorf("redelivery of an answered run acknowledged %d times, want 1", acks.acks())
	}
	if got := onlyPausedRow(t, second, taskID); got.Status != "cancelled" {
		t.Errorf("approval of an answered run after its redelivery = %q, want cancelled", got.Status)
	}
	if row, err := second.runOfTask(t.Context(), taskID); err != nil || !runAnswered(row) {
		t.Errorf("stored run after the redelivery = %+v (err %v), want it kept as answered", row, err)
	}

	second.answer(t.Context(), pause.RequestID, "resumed", "answered", map[string]any{"approved": true})
	second.wg.Wait()
	if rec.executed() {
		t.Error("approving the retired pause ran the gated tool")
	}
	if executionFor(second, taskID) != nil {
		t.Error("approving the retired pause rebuilt the answered run")
	}
}

func unreachablePausedRepo(t *testing.T) *wfstore.PausedRepo {
	t.Helper()
	db, err := wfstore.Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), slog.Default())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return wfstore.NewPausedRepo(db)
}

func TestCheckpointResume_AnsweredRunIsDroppedWhenItsPauseCannotBeRetired(t *testing.T) {
	const taskID = "task-checkpoint-answered-unretired"
	first, pause := answeredWhilePaused(t, taskID)
	second, rec := restartedHILWorkflow(t, first)
	second.pausedRepo = unreachablePausedRepo(t)

	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, true)
	second.handleTaskRequest(t.Context(), *req, msg)
	second.wg.Wait()

	if executionFor(second, taskID) != nil {
		t.Fatal("redelivery of an answered run restored it")
	}
	if acks.acks() != 1 {
		t.Errorf("redelivery of an answered run acknowledged %d times, want 1", acks.acks())
	}
	second.pausedRepo = first.pausedRepo
	second.answer(t.Context(), pause.RequestID, "resumed", "answered", map[string]any{"approved": true})
	second.wg.Wait()
	if rec.executed() {
		t.Error("approving the pause of an answered run ran the gated tool")
	}
	if got := onlyPausedRow(t, second, taskID); got.Status != "cancelled" {
		t.Errorf("pause of an answered run once the store works = %q, want cancelled", got.Status)
	}
}

func TestCheckpointResume_AnsweredRunRefusesToResumeItsPause(t *testing.T) {
	tests := []struct {
		name        string
		claimStatus string
		nodeStatus  string
	}{
		{name: "a person's answer", claimStatus: "resumed", nodeStatus: "answered"},
		{name: "the timeout sweep", claimStatus: "timed_out", nodeStatus: "timed_out"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const taskID = "task-checkpoint-answered-resume"
			first, pause := answeredWhilePaused(t, taskID)
			second, rec := restartedHILWorkflow(t, first)

			second.answer(t.Context(), pause.RequestID, tc.claimStatus, tc.nodeStatus, map[string]any{"approved": true})
			second.wg.Wait()

			if rec.executed() {
				t.Error("resuming the pause of an answered run ran the gated tool")
			}
			if executionFor(second, taskID) != nil {
				t.Error("resuming the pause of an answered run rebuilt it")
			}
			if got := onlyPausedRow(t, second, taskID); got.Status != "cancelled" {
				t.Errorf("pause of an answered run after %s = %q, want cancelled", tc.name, got.Status)
			}
		})
	}
}

func TestRestoreMetadataTypes_MapCountersSurviveASnapshot(t *testing.T) {
	nodes := []*NodeConfig{
		{ID: "fanout", Type: "map", ItemsExpression: []any{"a", "b"}, TemplateNode: "item"},
		{ID: "item", Type: "agent", AgentName: "itemWorker"},
	}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	state := NewExecutionState("wf", "e1")
	state.Metadata["fanout_done"] = []bool{true, false}
	state.Metadata["fanout_completed"] = 1
	state.Metadata["fanout_total"] = 2
	state.Metadata["fanout_concurrency"] = 2
	state.Metadata[finalKeyDurationNs] = int64(1500)
	raw, _ := json.Marshal(state)
	var restored WorkflowExecutionState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	restoreMetadataTypes(restored.Metadata, dag)

	if done, ok := restored.Metadata["fanout_done"].([]bool); !ok || !done[0] || done[1] {
		t.Errorf("_done = %#v, want []bool{true, false}", restored.Metadata["fanout_done"])
	}
	for key, want := range map[string]int{"fanout_completed": 1, "fanout_total": 2, "fanout_concurrency": 2} {
		if got, ok := restored.Metadata[key].(int); !ok || got != want {
			t.Errorf("%s = %#v, want int %d", key, restored.Metadata[key], want)
		}
	}
	if got, ok := restored.Metadata[finalKeyDurationNs].(int64); !ok || got != 1500 {
		t.Errorf("duration = %#v, want int64 1500", restored.Metadata[finalKeyDurationNs])
	}
	if strings.Contains(string(raw), "fanout_items") {
		t.Fatal("fixture unexpectedly carries items")
	}
}
