// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const (
	pausedRowExecution = "0190a1b2-0000-7000-8000-0000000c0001"
	otherExecution     = "0190a1b2-0000-7000-8000-0000000c0002"
)

func encodedState(t *testing.T, executionID string, completed ...string) string {
	t.Helper()
	return encodedPausedState(t, executionID, false, nil, completed...)
}

func encodedPausedState(t *testing.T, executionID string, hasPausedRows bool, pending []string, completed ...string) string {
	t.Helper()
	state := NewExecutionState("four-steps", executionID)
	state.HasPausedRows = hasPausedRows
	for _, node := range pending {
		state.PendingNodes[node] = true
	}
	for _, node := range completed {
		state.CompletedNodes[node] = "sub-" + node
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return string(raw)
}

func encodedExecContext(t *testing.T, taskID string) string {
	t.Helper()
	raw, err := json.Marshal(persistedExecContext{TaskID: taskID, UserProps: a2a.UserProperties{UserID: "alice", ReplyTo: checkpointReplyTo}})
	if err != nil {
		t.Fatalf("marshal exec context: %v", err)
	}
	return string(raw)
}

func insertCheckpoint(t *testing.T, a *checkpointAWE, row testCheckpoint) {
	t.Helper()
	putCheckpoint(t, a.w.checkpointStore(), row)
}

func TestPurgeExpiredCheckpoints_RemovesOnlyRunsThatOutlivedTheirKeep(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	now := time.Now()
	longPast := now.Add(-checkpointDeadlineMargin - time.Minute).UnixMilli()
	recentlyPast := now.Add(-checkpointDeadlineMargin + time.Minute).UnixMilli()
	rows := []struct {
		task        string
		deadline    *int64
		status      string
		endedFor    time.Duration
		idleFor     time.Duration
		wantRemoved bool
	}{
		{task: "held-deadline-past-the-margin", deadline: &longPast, status: wfstore.RunHeld, wantRemoved: true},
		{task: "held-deadline-within-the-margin", deadline: &recentlyPast, status: wfstore.RunHeld, wantRemoved: false},
		{task: "held-no-deadline", status: wfstore.RunHeld, wantRemoved: false},
		{task: "held-no-deadline-idle", status: wfstore.RunHeld, idleFor: checkpointMaxIdle + time.Minute, wantRemoved: true},
		{task: "parked-no-deadline-idle", status: wfstore.RunParked, idleFor: checkpointMaxIdle + time.Minute, wantRemoved: false},
		{task: "parked-deadline-past-the-margin", deadline: &longPast, status: wfstore.RunParked, wantRemoved: false},
		{task: "ended-long-ago", status: wfstore.RunEnded, endedFor: resolvedPausedAge + time.Minute, wantRemoved: true},
		{task: "ended-recently", status: wfstore.RunEnded, endedFor: time.Minute, wantRemoved: false},
	}
	for i, row := range rows {
		executionID := "0190a1b2-0000-7000-8000-00000000d00" + string(rune('0'+i))
		insertCheckpoint(t, awe, testCheckpoint{
			TaskID: row.task, ExecutionID: executionID, WorkflowName: "four-steps",
			StateSnapshot: "{}", ExecContext: "{}", DeadlineAt: row.deadline,
		})
		if _, err := awe.w.pausedDB.ExecContext(t.Context(),
			`UPDATE workflow_checkpoint SET status = ?, ended_at = ?, updated_at = ? WHERE task_id = ?`,
			row.status, now.Add(-row.endedFor).UnixMilli(), now.Add(-row.idleFor).UnixMilli(), row.task); err != nil {
			t.Fatalf("age stored run: %v", err)
		}
	}

	awe.w.purgeExpiredCheckpoints(t.Context())

	for _, row := range rows {
		removed := checkpointOf(t, awe, row.task) == nil
		if removed != row.wantRemoved {
			t.Errorf("stored run %q removed = %v, want %v", row.task, removed, row.wantRemoved)
		}
	}
}

func TestCheckpointResume_UnreadableCheckpointStartsTheRunOver(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0901"
	insertCheckpoint(t, awe, testCheckpoint{
		TaskID: taskID, ExecutionID: otherExecution, WorkflowName: "four-steps", DefinitionHash: awe.w.definitionHash,
		StateSnapshot: "{not json", ExecContext: encodedExecContext(t, taskID),
	})

	var acks ackCounter
	awe.deliver(t, taskID, &acks, true)

	if got := awe.sendsTo("agent1"); got != 1 {
		t.Fatalf("n1 dispatched %d times after an unreadable checkpoint, want 1: the run starts over", got)
	}
	row := checkpointOf(t, awe, taskID)
	if row == nil || row.ExecutionID == otherExecution {
		t.Errorf("checkpoint after the restart = %+v, want the new run's in place of the unreadable one", row)
	}
}

func TestCheckpointResume_ChangedDefinitionStartsTheRunOver(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0906"
	insertCheckpoint(t, awe, testCheckpoint{
		TaskID: taskID, ExecutionID: otherExecution, WorkflowName: "four-steps", DefinitionHash: "written-under-an-older-definition",
		StateSnapshot: encodedState(t, otherExecution, "n1", "n2"), ExecContext: encodedExecContext(t, taskID),
	})

	var acks ackCounter
	awe.deliver(t, taskID, &acks, true)

	if got := awe.sendsTo("agent1"); got != 1 {
		t.Fatalf("n1 dispatched %d times for a checkpoint of another definition, want 1: the run starts over", got)
	}
	if row := checkpointOf(t, awe, taskID); row == nil || row.ExecutionID == otherExecution {
		t.Errorf("checkpoint after the restart = %+v, want the new run's in place of the outdated one", row)
	}
}

func TestCheckpointResume_UnreachableStoreStartsTheRunOver(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	if err := awe.w.pausedDB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	const taskID = "019e0000-0000-7000-8000-00000c0e0902"
	var acks ackCounter
	awe.deliver(t, taskID, &acks, true)

	if got := awe.sendsTo("agent1"); got != 1 {
		t.Fatalf("n1 dispatched %d times when the checkpoint store cannot be read, want 1", got)
	}
}

func TestCheckpoint_StoreFailureMidRunDoesNotStopTheRun(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0903"
	var acks ackCounter
	awe.deliver(t, taskID, &acks, false)
	awe.answer(t, "agent1", "output-of-n1")
	if err := awe.w.pausedDB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	awe.answer(t, "agent2", "output-of-n2")
	awe.answer(t, "agent3", "output-of-n3")
	awe.answer(t, "agent4", "output-of-n4")

	final := finalTaskOn(t, awe.brk.snapshot(), checkpointReplyTo)
	if final == nil || final.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("final response with a failing checkpoint store = %+v, want completed", final)
	}
	if acks.acks() != 1 {
		t.Errorf("request acknowledged %d times, want 1", acks.acks())
	}
}

func TestCheckpointResume_StoppingInstanceLeavesTheRestoredRunForAnotherAWE(t *testing.T) {
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0904"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "agent1", "output-of-n1")
	first.kill()

	second := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	second.w.executionsMu.Lock()
	second.w.stopping = true
	second.w.executionsMu.Unlock()
	second.deliver(t, taskID, &secondAcks, true)

	if got := second.sendsTo("agent2"); got != 0 {
		t.Errorf("a stopping instance dispatched the restored run's step %d times, want 0", got)
	}
	if secondAcks.acks() != 0 {
		t.Errorf("request left for another AWE acknowledged %d times, want 0", secondAcks.acks())
	}
	if row := checkpointOf(t, second, taskID); row == nil || row.Status != wfstore.RunHeld || row.HeldBy != wfstore.HeldByRequest {
		t.Errorf("stored run = %+v, want it still held by its request", row)
	}
}

func TestWriteCheckpoint_StateOverTheCapIsNotWritten(t *testing.T) {
	db, err := wfstore.Open("sqlite:///"+filepath.Join(t.TempDir(), "cap.db"), slog.Default())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if migrateErr := db.Migrate(t.Context()); migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}
	repo := wfstore.NewRunRepo(db)
	w := &WorkflowInstance{checkpointRepo: repo, logger: slog.Default()}
	state := NewExecutionState("cap", "0190a1b2-0000-7000-8000-0000000ca900")
	state.NodeOutputs["big"] = map[string]any{"output": strings.Repeat("x", maxCheckpointBytes)}
	execCtx := NewExecutionContext(t.Context(), "cap-task", "ctx", nil, a2a.UserProperties{UserID: "alice"}, false)
	defer execCtx.Cancel()
	e := &DAGExecutor{cfg: &WorkflowConfig{Name: "cap"}, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}

	e.writeCheckpointLocked()

	row, err := repo.Get(t.Context(), state.ExecutionID)
	if err != nil || row != nil {
		t.Fatalf("checkpoint of a state over the cap = %+v (err %v), want none", row, err)
	}
	if !e.checkpoint.unwritable {
		t.Errorf("cursor after an oversized state = %+v, want checkpointing stopped", e.checkpoint)
	}
}

func requestIDsSentTo(t *testing.T, msgs []capturedMessage, topic string) []string {
	t.Helper()
	var ids []string
	for _, m := range msgs {
		if m.Topic != topic || !strings.Contains(string(m.Payload), `"message/send"`) {
			continue
		}
		var req a2a.Request
		if err := json.Unmarshal(m.Payload, &req); err != nil {
			t.Fatalf("unmarshal agent request: %v", err)
		}
		ids = append(ids, req.ID)
	}
	return ids
}

func (a *checkpointAWE) answerEvery(t *testing.T, agentName string, answered map[string]bool) {
	t.Helper()
	topic := a2a.AgentRequestTopic("test-ns", agentName)
	for {
		progressed := false
		for _, id := range requestIDsSentTo(t, a.brk.snapshot(), topic) {
			if answered[id] {
				continue
			}
			answered[id] = true
			progressed = true
			deliverSubTaskText(t, a.w, id, a2a.TaskStateCompleted, "answer-"+id)
		}
		if !progressed {
			return
		}
	}
}

func TestCheckpoint_FinalResponseIsMarkedAfterItIsPublishedAndBeforeTheAck(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0a01"
	committed := func() bool {
		row, err := awe.w.runOfTask(t.Context(), taskID)
		return err == nil && runAnswered(row)
	}
	var markedAtPublish, markedAtAck, published, acked bool
	awe.brk.publishHook = func(topic string) error {
		if topic == checkpointReplyTo {
			published, markedAtPublish = true, committed()
		}
		return nil
	}
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: checkpointReplyTo})
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() { acked, markedAtAck = true, committed() }, func() {})
	msg.UserProperties = bmsg.UserProperties
	awe.w.handleTaskRequest(t.Context(), *req, msg)

	for _, agent := range []string{"agent1", "agent2", "agent3", "agent4"} {
		awe.answer(t, agent, "output-of-"+agent)
	}

	if !published || !acked {
		t.Fatalf("run published=%v acked=%v, want both", published, acked)
	}
	if markedAtPublish {
		t.Error("the final response was marked as sent before it was published; a death before the publish would lose it")
	}
	if !markedAtAck {
		t.Error("the final response was not marked as sent when the request was acknowledged; a death before the ack would answer twice")
	}
}

func deliverWithToken(t *testing.T, a *checkpointAWE, taskID, token string, redelivered bool) {
	t.Helper()
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: checkpointReplyTo, AuthToken: token})
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() {}, func() {})
	msg.UserProperties = bmsg.UserProperties
	msg.Redelivered = redelivered
	a.w.handleTaskRequest(t.Context(), *req, msg)
}

func TestCheckpointResume_TokenIsNeverStoredAndComesFromTheRedelivery(t *testing.T) {
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0a05"
	deliverWithToken(t, first, taskID, "token-of-the-first-delivery", false)
	first.answer(t, "agent1", "output-of-n1")
	row := checkpointOf(t, first, taskID)
	if row == nil {
		t.Fatal("no checkpoint written")
	}
	if strings.Contains(row.ExecContext, "token-of-the-first-delivery") || strings.Contains(row.StateSnapshot, "token-of-the-first-delivery") {
		t.Fatal("the caller's auth token is stored in the checkpoint")
	}
	first.kill()

	second := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	deliverWithToken(t, second, taskID, "token-of-the-redelivery", true)

	exec := executionFor(second.w, taskID)
	if exec == nil {
		t.Fatal("the run was not restored")
	}
	if got := exec.Context.UserProps.AuthToken; got != "token-of-the-redelivery" {
		t.Errorf("restored run's auth token = %q, want the redelivered request's", got)
	}
}

func TestCheckpointBreaker_PausesWritesAfterRepeatedFailuresUntilACooldownPasses(t *testing.T) {
	var b checkpointBreaker
	now := time.Now()
	for i := range checkpointBreakerFailures - 1 {
		if opened, _ := b.record(true, now); opened {
			t.Fatalf("breaker opened after %d failures, want %d", i+1, checkpointBreakerFailures)
		}
	}
	if !b.allow(now) {
		t.Fatal("writes paused before the failure threshold")
	}
	if opened, _ := b.record(true, now); !opened {
		t.Fatal("breaker did not open at the failure threshold")
	}
	if b.allow(now.Add(checkpointBreakerCooldown - time.Second)) {
		t.Error("writes allowed during the cooldown")
	}
	if !b.allow(now.Add(checkpointBreakerCooldown)) {
		t.Error("writes still paused after the cooldown")
	}
	if _, closed := b.record(false, now.Add(checkpointBreakerCooldown)); !closed {
		t.Error("a successful write after the cooldown did not close the breaker")
	}
	if !b.allow(now.Add(checkpointBreakerCooldown)) {
		t.Error("writes paused after the breaker closed")
	}
}

func TestCheckpointPurgeLoop_RunsOnItsInterval(t *testing.T) {
	cfg := fourStepWorkflow()
	cfg.HILDatabaseURL = sharedStoreURL(t)
	w := NewWorkflowInstance(cfg)
	w.checkpointPurgeEvery = 20 * time.Millisecond
	if err := w.Init(t.Context(), nil, &stubServices{brk: &capturingBroker{}, features: checkpointFlag()}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
	past := time.Now().Add(-checkpointDeadlineMargin - time.Minute).UnixMilli()
	putCheckpoint(t, w.checkpointStore(), testCheckpoint{
		TaskID: "abandoned", ExecutionID: otherExecution, WorkflowName: "four-steps",
		StateSnapshot: "{}", ExecContext: "{}", DeadlineAt: &past,
	})

	waitFor(t, "the purge to remove the abandoned checkpoint", func() bool {
		row, err := w.runOfTask(t.Context(), "abandoned")
		return err == nil && row == nil
	})
}

func TestCheckpointPurgeLoop_PurgesSoonAfterStart(t *testing.T) {
	tests := []struct {
		name  string
		flags []string
	}{
		{name: "checkpoint resume on", flags: []string{features.KeyWorkflowCheckpointResume}},
		{name: "only HIL pause on, with rows left from before checkpoint resume was turned off", flags: []string{"workflow_hil_pause_resume"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fourStepWorkflow()
			cfg.HILDatabaseURL = sharedStoreURL(t)
			w := NewWorkflowInstance(cfg)
			w.checkpointPurgeEvery = time.Hour
			w.checkpointPurgeFirstWithin = 20 * time.Millisecond
			if err := w.Init(t.Context(), nil, &stubServices{brk: &capturingBroker{}, features: flagsOn(tc.flags...)}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			past := time.Now().Add(-checkpointDeadlineMargin - time.Minute).UnixMilli()
			putCheckpoint(t, w.runRepo(), testCheckpoint{
				TaskID: "abandoned", ExecutionID: otherExecution, WorkflowName: "four-steps",
				StateSnapshot: "{}", ExecContext: "{}", DeadlineAt: &past,
			})
			if err := w.Start(t.Context()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() { _ = w.Stop(context.Background()) })

			waitFor(t, "the first purge to remove the abandoned checkpoint", func() bool {
				if w.runRepo() == nil {
					return false
				}
				row, err := w.runOfTask(t.Context(), "abandoned")
				return err == nil && row == nil
			})
		})
	}
}

func TestOpenPausedStore_EachFlagOpensOnlyItsOwnStore(t *testing.T) {
	tests := []struct {
		name           string
		flags          []string
		wantPaused     bool
		wantCheckpoint bool
	}{
		{name: "checkpoint resume alone does not enable HIL pause", flags: []string{features.KeyWorkflowCheckpointResume}, wantPaused: false, wantCheckpoint: true},
		{name: "HIL pause alone does not checkpoint", flags: []string{"workflow_hil_pause_resume"}, wantPaused: true, wantCheckpoint: false},
		{name: "both flags open both stores", flags: []string{"workflow_hil_pause_resume", features.KeyWorkflowCheckpointResume}, wantPaused: true, wantCheckpoint: true},
		{name: "neither flag opens nothing", flags: nil, wantPaused: false, wantCheckpoint: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fourStepWorkflow()
			cfg.HILDatabaseURL = sharedStoreURL(t)
			w := NewWorkflowInstance(cfg)
			if err := w.Init(t.Context(), nil, &stubServices{brk: &capturingBroker{}, features: flagsOn(tc.flags...)}); err != nil {
				t.Fatalf("Init: %v", err)
			}
			t.Cleanup(w.closePausedStore)

			if got := w.pausedStore() != nil; got != tc.wantPaused {
				t.Errorf("paused store open = %v, want %v", got, tc.wantPaused)
			}
			if got := w.checkpointStore() != nil; got != tc.wantCheckpoint {
				t.Errorf("checkpoint store open = %v, want %v", got, tc.wantCheckpoint)
			}
		})
	}
}

func TestOpenPausedStore_UnusableStoreStopsTheWorkflowOnlyWhenHILNeedsIt(t *testing.T) {
	tests := []struct {
		name        string
		flags       []string
		wantInitErr bool
	}{
		{name: "checkpoint resume alone runs without checkpoints", flags: []string{features.KeyWorkflowCheckpointResume}, wantInitErr: false},
		{name: "HIL pause fails the workflow as before", flags: []string{"workflow_hil_pause_resume", features.KeyWorkflowCheckpointResume}, wantInitErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocker := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(blocker, nil, 0o600); err != nil {
				t.Fatalf("write blocker: %v", err)
			}
			cfg := fourStepWorkflow()
			cfg.HILDatabaseURL = "sqlite:///" + filepath.Join(blocker, "wf.db")
			w := NewWorkflowInstance(cfg)
			err := w.Init(t.Context(), nil, &stubServices{brk: &capturingBroker{}, features: flagsOn(tc.flags...)})
			t.Cleanup(w.closePausedStore)

			if (err != nil) != tc.wantInitErr {
				t.Fatalf("Init error = %v, want error %v", err, tc.wantInitErr)
			}
			if w.checkpointStore() != nil {
				t.Error("a store that could not be opened is in use")
			}
		})
	}
}

func TestCheckpointResume_MapRelaunchThatFailsToPublishStillRunsEveryItemOnce(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "fanout",
			Namespace: "test-ns",
			Nodes: []*NodeConfig{
				{ID: "fanout", Type: "map", ItemsExpression: []any{"a", "b", "c", "d", "e"}, TemplateNode: "item", ConcurrencyLimit: 3},
				{ID: "item", Type: "agent", AgentName: "itemWorker"},
			},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0a03"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	if got := first.sendsTo("itemWorker"); got != 3 {
		t.Fatalf("first process launched %d branches, want the concurrency limit of 3", got)
	}
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	workerTopic := a2a.AgentRequestTopic("test-ns", "itemWorker")
	const cancelsOfSupersededBranches = 3
	workerPublishes := 0
	second.brk.publishHook = func(topic string) error {
		if topic != workerTopic {
			return nil
		}
		workerPublishes++
		if workerPublishes == cancelsOfSupersededBranches+1 {
			return errors.New("broker unavailable")
		}
		return nil
	}
	second.deliver(t, taskID, &secondAcks, true)
	second.answerEvery(t, "itemWorker", map[string]bool{})

	if got := second.sendsTo("itemWorker"); got != 4 {
		t.Errorf("restored map sent %d requests, want 4: the two relaunched branches and the two items never launched", got)
	}
	if finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) == nil {
		t.Fatal("restored map never finished")
	}
}

func TestCheckpointResume_RunPastItsDeadlineDispatchesNothing(t *testing.T) {
	cfg := func() *WorkflowConfig {
		c := fourStepWorkflow()
		c.WorkflowTimeout = 200 * time.Millisecond
		return c
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0a04"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	exec := executionFor(first.w, taskID)
	exec.Executor.mu.Lock()
	deadline := exec.State.WorkflowDeadline
	exec.Executor.mu.Unlock()
	first.kill()
	waitFor(t, "the deadline to pass", func() bool { return time.Now().After(deadline) })

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)

	starts := 0
	for _, m := range second.brk.snapshot() {
		if m.Topic == checkpointStatusTopic && strings.Contains(string(m.Payload), string(a2a.SignalWorkflowNodeExecStart)) {
			starts++
		}
	}
	if got := second.sendsTo("agent1"); got != 0 || starts != 0 {
		t.Errorf("a run restored past its deadline dispatched n1 %d times and started %d nodes, want neither", got, starts)
	}
	waitFor(t, "the restored run to end", func() bool {
		return finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) != nil
	})
}

func TestWorkflowDefinitionHash_ChangesOnlyWithTheGraph(t *testing.T) {
	base := func() []*NodeConfig {
		return []*NodeConfig{
			{ID: "pick", Type: "switch", Cases: []SwitchCase{{Condition: "1 == 1", Node: "a"}}, DefaultCase: "b"},
			{ID: "a", Type: "agent", AgentName: "agentA", DependsOn: []string{"pick"}, Instruction: "do a", Input: map[string]any{"x": "{{workflow.input.x}}"}},
			{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"pick"}},
		}
	}
	reference := workflowDefinitionHash(&WorkflowConfig{Nodes: base(), OnExit: &ExitHandler{Always: "b"}})
	tests := []struct {
		name     string
		nodes    []*NodeConfig
		onExit   *ExitHandler
		wantSame bool
	}{
		{name: "the same graph in another order", nodes: []*NodeConfig{base()[2], base()[0], base()[1]}, onExit: &ExitHandler{Always: "b"}, wantSame: true},
		{name: "a changed instruction, input and target", nodes: []*NodeConfig{base()[0], {ID: "a", Type: "agent", AgentName: "agentZ", DependsOn: []string{"pick"}, Instruction: "do it differently", Input: map[string]any{"y": 1}}, base()[2]}, onExit: &ExitHandler{Always: "b"}, wantSame: true},
		{name: "a node added", nodes: append(base(), &NodeConfig{ID: "c", Type: "agent", AgentName: "agentC"}), onExit: &ExitHandler{Always: "b"}, wantSame: false},
		{name: "a dependency changed", nodes: []*NodeConfig{base()[0], base()[1], {ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"a"}}}, onExit: &ExitHandler{Always: "b"}, wantSame: false},
		{name: "a switch target changed", nodes: []*NodeConfig{{ID: "pick", Type: "switch", Cases: []SwitchCase{{Condition: "1 == 1", Node: "b"}}, DefaultCase: "a"}, base()[1], base()[2]}, onExit: &ExitHandler{Always: "b"}, wantSame: false},
		{name: "an exit handler changed", nodes: base(), onExit: &ExitHandler{OnSuccess: "b"}, wantSame: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowDefinitionHash(&WorkflowConfig{Nodes: tc.nodes, OnExit: tc.onExit})
			if (got == reference) != tc.wantSame {
				t.Errorf("hash equal to the reference = %v, want %v", got == reference, tc.wantSame)
			}
		})
	}
}

func TestCheckpointResume_CancelledRunKilledInItsExitHandlerFinishesTheHandler(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "cancel-exit",
			Namespace: "test-ns",
			OnExit:    &ExitHandler{Always: "cleanup"},
			Nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"},
			},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0b01"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	_ = executionFor(first.w, taskID).Executor.Advance(t.Context(), &EventCancel{Reason: "user"})
	if got := first.sendsTo("cleanupAgent"); got != 1 {
		t.Fatalf("exit handler not in flight before the kill: %d sends", got)
	}
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	if got := second.sendsTo("cleanupAgent"); got != 1 {
		t.Fatalf("exit handler of a cancelled run dispatched %d times after the restore, want 1", got)
	}
	if got := second.sendsTo("agentA"); got != 0 {
		t.Errorf("cancelled node dispatched %d times after the restore, want 0", got)
	}
	second.answer(t, "cleanupAgent", "cleaned")

	if finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) == nil || secondAcks.acks() != 1 {
		t.Errorf("cancelled run after its handler: answered=%v acks=%d, want answered and 1",
			finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) != nil, secondAcks.acks())
	}
}

func TestCheckpointResume_ExitChainDoesNotRedispatchTheMainFlow(t *testing.T) {
	cfg := func() *WorkflowConfig {
		return &WorkflowConfig{
			Name:      "failfast-exit",
			Namespace: "test-ns",
			FailFast:  true,
			OnExit:    &ExitHandler{Always: "cleanup"},
			Nodes: []*NodeConfig{
				{ID: "a", Type: "agent", AgentName: "agentA"},
				{ID: "b", Type: "agent", AgentName: "agentB"},
				{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"},
			},
		}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0b02"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	failed := lastRequestIDSentTo(t, first.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "agentA"))
	deliverSubTaskText(t, first.w, failed, a2a.TaskStateFailed, "broken")
	if got := first.sendsTo("cleanupAgent"); got != 1 {
		t.Fatalf("exit handler not in flight before the kill: %d sends", got)
	}
	first.kill()

	second := startCheckpointAWE(t, cfg(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)

	if got := second.sendsTo("agentB"); got != 0 {
		t.Errorf("main-flow node still pending in the exit chain dispatched %d times after the restore, want 0", got)
	}
	if got := second.sendsTo("cleanupAgent"); got != 1 {
		t.Errorf("exit handler in flight dispatched %d times after the restore, want 1", got)
	}
	group, attempt := lastRetryIdentitySentTo(t, second.brk.snapshot(), a2a.AgentRequestTopic("test-ns", "cleanupAgent"))
	if group == "" || attempt != 1 {
		t.Errorf("restored exit handler retry identity %q/%d, want a group at attempt 1", group, attempt)
	}
}

func TestCheckpoint_PurgedRowOfALiveRunIsWrittenAgain(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0b04"
	var acks ackCounter
	awe.deliver(t, taskID, &acks, false)
	awe.answer(t, "agent1", "output-of-n1")
	exec := executionFor(awe.w, taskID)
	if _, err := awe.w.pausedDB.ExecContext(t.Context(),
		`DELETE FROM workflow_checkpoint WHERE execution_id = ?`, exec.State.ExecutionID); err != nil {
		t.Fatalf("purge: %v", err)
	}

	awe.answer(t, "agent2", "output-of-n2")

	stored := checkpointOf(t, awe, taskID)
	if stored == nil || !strings.Contains(stored.StateSnapshot, `"n2"`) {
		t.Errorf("checkpoint after a purge = %+v, want it written again with n2", stored)
	}
}

func TestCheckpointResume_AnsweredRunUnderAChangedDefinitionIsDropped(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0b05"
	insertCheckpoint(t, awe, testCheckpoint{
		TaskID: taskID, ExecutionID: otherExecution, WorkflowName: "four-steps", DefinitionHash: "written-under-an-older-definition",
		StateSnapshot: `{"execution_id":"` + otherExecution + `"}`, ExecContext: encodedExecContext(t, taskID),
	})
	if err := markAnswered(t, awe.w.checkpointStore(), otherExecution); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	var acks ackCounter
	awe.deliver(t, taskID, &acks, true)

	if got := awe.sendsTo("agent1"); got != 0 || acks.acks() != 1 {
		t.Errorf("redelivery of an answered run under a changed definition: n1 sends %d, acks %d; want 0 and 1", got, acks.acks())
	}
	if !runAnswered(checkpointOf(t, awe, taskID)) {
		t.Error("the answered run's row lost its answered mark")
	}
}

func TestCheckpointResume_IdentityCredentialsAreNotStoredAndComeFromTheRedelivery(t *testing.T) {
	withIdentity := func(token string) *stubServices {
		return &stubServices{brk: &capturingBroker{}, features: checkpointFlag(), authn: &fakeAuthn{identity: &auth.Identity{
			UserID: "alice", Scopes: []string{"*"}, AuthToken: token, Claims: map[string]any{"secret_claim": "claim-of-" + token},
		}}}
	}
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWEWith(t, fourStepWorkflow(), dbURL, withIdentity("jwt-of-the-first-delivery"))
	const taskID = "019e0000-0000-7000-8000-00000c0e0b06"
	deliverWithToken(t, first, taskID, "jwt-of-the-first-delivery", false)
	first.answer(t, "agent1", "output-of-n1")
	row := checkpointOf(t, first, taskID)
	for _, secret := range []string{"jwt-of-the-first-delivery", "secret_claim"} {
		if strings.Contains(row.ExecContext, secret) || strings.Contains(row.StateSnapshot, secret) {
			t.Errorf("checkpoint stores %q from the caller's identity", secret)
		}
	}
	if !strings.Contains(row.ExecContext, "alice") {
		t.Error("checkpoint lost the caller's user id")
	}
	first.kill()

	second := startCheckpointAWEWith(t, fourStepWorkflow(), dbURL, withIdentity("jwt-of-the-redelivery"))
	deliverWithToken(t, second, taskID, "jwt-of-the-redelivery", true)

	exec := executionFor(second.w, taskID)
	if exec == nil || exec.Context.Identity == nil || exec.Context.Identity.AuthToken != "jwt-of-the-redelivery" {
		t.Fatalf("restored run's identity = %+v, want the one authenticated from the redelivery", exec)
	}
}

func TestCheckpoint_OversizedStateKeepsTheLastWriteAndStillMarksTheAnswer(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0b07"
	var markedAtAck bool
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: checkpointReplyTo})
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() {
		row, err := awe.w.runOfTask(t.Context(), taskID)
		markedAtAck = err == nil && row != nil && runAnswered(row)
	}, func() {})
	msg.UserProperties = bmsg.UserProperties
	awe.w.handleTaskRequest(t.Context(), *req, msg)
	awe.answer(t, "agent1", "output-of-n1")
	if checkpointOf(t, awe, taskID) == nil {
		t.Fatal("no checkpoint before the state outgrew the cap")
	}

	before := checkpointOf(t, awe, taskID).StateSnapshot
	awe.answer(t, "agent2", strings.Repeat("x", maxCheckpointBytes))
	if row := checkpointOf(t, awe, taskID); row == nil || row.StateSnapshot != before {
		t.Fatal("a state over the cap was written, or the last state written was lost")
	}
	awe.answer(t, "agent3", "output-of-n3")
	awe.answer(t, "agent4", "output-of-n4")

	if !markedAtAck {
		t.Error("a run over the cap was not marked as answered when its request was acknowledged")
	}
}

func TestCheckpoint_RepeatedFailedWritesPauseCheckpointing(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0b09"
	var acks ackCounter
	awe.deliver(t, taskID, &acks, false)
	if err := awe.w.pausedDB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	for _, agent := range []string{"agent1", "agent2", "agent3"} {
		awe.answer(t, agent, "output-of-"+agent)
	}

	if awe.w.checkpointBreaker.allow(time.Now()) {
		t.Error("checkpoint writes still attempted after repeated failures")
	}
	awe.answer(t, "agent4", "output-of-agent4")
	if finalTaskOn(t, awe.brk.snapshot(), checkpointReplyTo) == nil {
		t.Error("the run did not finish while checkpoint writes were paused")
	}
	awe.w.checkpointBreaker.mu.Lock()
	failures := awe.w.checkpointBreaker.failures
	awe.w.checkpointBreaker.mu.Unlock()
	if failures != checkpointBreakerFailures {
		t.Errorf("%d failed checkpoint writes, want the store left alone after the %dth", failures, checkpointBreakerFailures)
	}
}

func TestCheckpointResume_ToolNodeInFlightRunsAgain(t *testing.T) {
	first, firstTool := newHILWorkflow(t, gatedCallNode())
	first.hilToolConfigs = nil
	first.tools.SetHILConfigs(nil)
	first.checkpointRepo = wfstore.NewRunRepo(first.pausedDB)
	firstTool.block = make(chan struct{})
	t.Cleanup(func() { close(firstTool.block) })
	firstCtx, killFirst := context.WithCancel(t.Context())
	first.ctx = firstCtx
	const taskID = "task-checkpoint-tool"
	var firstAcks, secondAcks ackCounter
	req, msg := deliveredRequest(taskID, &firstAcks, false)
	first.handleTaskRequest(t.Context(), *req, msg)
	firstTool.waitUntilRunning(t)
	first.processStopping.Store(true)
	killFirst()

	second, secondTool := newHILWorkflow(t, gatedCallNode())
	second.hilToolConfigs = nil
	second.tools.SetHILConfigs(nil)
	second.pausedDB, second.pausedRepo = first.pausedDB, first.pausedRepo
	second.checkpointRepo = first.checkpointRepo
	req, msg = deliveredRequest(taskID, &secondAcks, true)
	second.handleTaskRequest(t.Context(), *req, msg)
	second.wg.Wait()

	if !secondTool.executed() {
		t.Fatal("the tool node in flight at the kill did not run again")
	}
	if secondAcks.acks() != 1 {
		t.Errorf("restored tool run acknowledged %d times, want 1", secondAcks.acks())
	}
}

func TestCheckpointResume_StopFailsARestoredRunToItsCaller(t *testing.T) {
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0c01"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "agent1", "output-of-n1")
	first.kill()
	second := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)
	if executionFor(second.w, taskID) == nil {
		t.Fatal("the run was not restored")
	}

	if err := second.w.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if finalTaskOn(t, second.brk.snapshot(), checkpointReplyTo) == nil {
		t.Error("a restored run left running at Stop was not failed to its caller")
	}
}

func (a *checkpointAWE) deliverAs(t *testing.T, taskID, user string, acks *ackCounter, redelivered bool) {
	t.Helper()
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: user, ReplyTo: checkpointReplyTo, AuthToken: "token-of-" + user})
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() { acks.n.Add(1) }, func() {})
	msg.UserProperties = bmsg.UserProperties
	msg.Redelivered = redelivered
	a.w.handleTaskRequest(t.Context(), *req, msg)
}

func TestCheckpointResume_OnlyTheRunsUserRestoresIt(t *testing.T) {
	tests := []struct {
		name         string
		deliveredBy  string
		wantRestored bool
	}{
		{name: "the run's own user restores it", deliveredBy: "alice", wantRestored: true},
		{name: "another user is refused", deliveredBy: "mallory", wantRestored: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbURL := sharedStoreURL(t)
			first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
			const taskID = "019e0000-0000-7000-8000-00000c0e0c02"
			var firstAcks, secondAcks ackCounter
			first.deliverAs(t, taskID, "alice", &firstAcks, false)
			first.answer(t, "agent1", "output-of-n1")
			first.kill()
			before := checkpointOf(t, first, taskID)

			second := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
			second.deliverAs(t, taskID, tc.deliveredBy, &secondAcks, true)

			restored := executionFor(second.w, taskID) != nil
			after := checkpointOf(t, second, taskID)
			if restored != tc.wantRestored || secondAcks.acks() != 0 && tc.wantRestored || after == nil {
				t.Errorf("delivered by %s: restored=%v acks=%d checkpoint kept=%v; want restored=%v",
					tc.deliveredBy, restored, secondAcks.acks(), after != nil, tc.wantRestored)
			}
			if got := second.sendsTo("agent2"); (got == 1) != tc.wantRestored {
				t.Errorf("delivered by %s: n2 dispatched %d times", tc.deliveredBy, got)
			}
			if !tc.wantRestored && (after.UpdatedAt != before.UpdatedAt || after.StateSnapshot != before.StateSnapshot || secondAcks.acks() != 1) {
				t.Errorf("refused delivery rewrote the checkpoint or was not acknowledged (%d)", secondAcks.acks())
			}
		})
	}
}

func TestCheckpointResume_AnotherUsersDeliveryLeavesTheRowForTheOwner(t *testing.T) {
	tests := []struct {
		name              string
		snapshot          string
		definitionHash    string
		answered          bool
		wantOwnerDispatch int
		wantRowAfterOwner bool
	}{
		{name: "an answered run's mark", snapshot: `{"execution_id":"` + otherExecution + `"}`, definitionHash: "", answered: true, wantOwnerDispatch: 0, wantRowAfterOwner: true},
		{name: "a run checkpointed under another definition", snapshot: `{"execution_id":"` + otherExecution + `","pending_nodes":{"n2":true}}`, definitionHash: "written-under-an-older-definition", wantOwnerDispatch: 1, wantRowAfterOwner: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
			const taskID = "019e0000-0000-7000-8000-00000c0e0d01"
			insertCheckpoint(t, awe, testCheckpoint{
				TaskID: taskID, ExecutionID: otherExecution, WorkflowName: "four-steps", DefinitionHash: tc.definitionHash,
				StateSnapshot: tc.snapshot, ExecContext: encodedExecContext(t, taskID),
			})
			if tc.answered {
				if err := markAnswered(t, awe.w.checkpointStore(), otherExecution); err != nil {
					t.Fatalf("mark answered: %v", err)
				}
			}
			before := checkpointOf(t, awe, taskID)

			var malloryAcks, aliceAcks ackCounter
			awe.deliverAs(t, taskID, "mallory", &malloryAcks, true)
			after := checkpointOf(t, awe, taskID)
			if after == nil || after.UpdatedAt != before.UpdatedAt || after.StateSnapshot != before.StateSnapshot || malloryAcks.acks() != 1 || awe.sendsTo("agent1") != 0 {
				t.Fatalf("another user's delivery: row kept=%v, acks %d, n1 sends %d; want the row untouched, one ack and no dispatch",
					after != nil, malloryAcks.acks(), awe.sendsTo("agent1"))
			}

			awe.deliverAs(t, taskID, "alice", &aliceAcks, true)
			if got := awe.sendsTo("agent1"); got != tc.wantOwnerDispatch {
				t.Errorf("owner's delivery dispatched n1 %d times, want %d", got, tc.wantOwnerDispatch)
			}
			if got := checkpointOf(t, awe, taskID) != nil; got != tc.wantRowAfterOwner {
				t.Errorf("checkpoint after the owner's delivery present = %v, want %v", got, tc.wantRowAfterOwner)
			}
		})
	}
}

func TestCheckpointResume_OnlyTheRunsAuthenticatedUserRestoresIt(t *testing.T) {
	tests := []struct {
		name          string
		authenticated string
		wantRestored  bool
	}{
		{name: "the run's authenticated user restores it", authenticated: "alice", wantRestored: true},
		{name: "another authenticated user is refused", authenticated: "mallory", wantRestored: false},
	}
	authenticatedAs := func(user string) *stubServices {
		return &stubServices{brk: &capturingBroker{}, features: checkpointFlag(), authn: &fakeAuthn{identity: &auth.Identity{
			UserID: user, Scopes: []string{"*"}, AuthToken: "jwt-of-" + user,
		}}}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbURL := sharedStoreURL(t)
			first := startCheckpointAWEWith(t, fourStepWorkflow(), dbURL, authenticatedAs("alice"))
			const taskID = "019e0000-0000-7000-8000-00000c0e0c03"
			deliverWithToken(t, first, taskID, "jwt-of-alice", false)
			first.answer(t, "agent1", "output-of-n1")
			first.kill()

			second := startCheckpointAWEWith(t, fourStepWorkflow(), dbURL, authenticatedAs(tc.authenticated))
			deliverWithToken(t, second, taskID, "jwt-of-"+tc.authenticated, true)

			if restored := executionFor(second.w, taskID) != nil; restored != tc.wantRestored {
				t.Errorf("authenticated as %s: restored=%v, want %v", tc.authenticated, restored, tc.wantRestored)
			}
		})
	}
}

func TestCheckpointResume_RunOfAChangedGraphStartsOver(t *testing.T) {
	dbURL := sharedStoreURL(t)
	first := startCheckpointAWE(t, fourStepWorkflow(), dbURL, checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0c04"
	var firstAcks, secondAcks ackCounter
	first.deliver(t, taskID, &firstAcks, false)
	first.answer(t, "agent1", "output-of-n1")
	first.kill()

	changed := fourStepWorkflow()
	changed.Nodes = append(changed.Nodes, &NodeConfig{ID: "n5", Type: "agent", AgentName: "agent5", DependsOn: []string{"n4"}})
	second := startCheckpointAWE(t, changed, dbURL, checkpointFlag())
	second.deliver(t, taskID, &secondAcks, true)

	if got := second.sendsTo("agent1"); got != 1 {
		t.Errorf("a run checkpointed under another graph dispatched n1 %d times on the new one, want 1: it starts over", got)
	}
}

func withNode(nodes []*NodeConfig, node *NodeConfig) []*NodeConfig {
	out := make([]*NodeConfig, 0, len(nodes))
	for _, n := range nodes {
		if n.ID == node.ID {
			out = append(out, node)
			continue
		}
		out = append(out, n)
	}
	return out
}

func hashGraph() []*NodeConfig {
	return []*NodeConfig{
		{ID: "pick", Type: "switch", Cases: []SwitchCase{{Condition: "1 == 1", Node: "a"}}, DefaultCase: "b"},
		{ID: "a", Type: "agent", AgentName: "agentA", DependsOn: []string{"pick", "route"}, Instruction: "do a", Input: map[string]any{"x": "{{workflow.input.x}}"}},
		{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"pick"}},
		{ID: "route", Type: "decision", Routes: map[string]string{"yes": "a", "no": "b"}, Escalate: "b", Default: "a", EscalationMode: "reroute"},
		{ID: "each", Type: "map", TemplateNode: "a"},
		{ID: "again", Type: "loop", BodyNode: "b", Condition: "1 == 1"},
	}
}

func hashExit() *ExitHandler {
	return &ExitHandler{Always: "b", OnSuccess: "a", OnFailure: "b", OnCancel: "a"}
}

func TestWorkflowDefinitionHash_PinnedForAFixedGraph(t *testing.T) {
	const want = "68b29a75c90a10863220fe24f8cdd656d33ead9473d91658b83d16cfcbdb526d"
	if got := workflowDefinitionHash(&WorkflowConfig{Nodes: hashGraph(), OnExit: hashExit()}); got != want {
		t.Errorf("hash of the fixed graph = %s, want %s; a change to what the fingerprint covers restarts every checkpointed run on upgrade, so bump definitionShapeVersion on purpose and update this", got, want)
	}
}

func TestWorkflowDefinitionHash_FollowsEachPartOfTheGraph(t *testing.T) {
	reference := workflowDefinitionHash(&WorkflowConfig{Nodes: hashGraph(), OnExit: hashExit()})
	base := hashGraph()
	tests := []struct {
		name     string
		nodes    []*NodeConfig
		onExit   *ExitHandler
		wantSame bool
	}{
		{name: "dependencies in another order", nodes: withNode(base, &NodeConfig{ID: "a", Type: "agent", AgentName: "agentA", DependsOn: []string{"route", "pick"}}), onExit: hashExit(), wantSame: true},
		{name: "a changed timeout and loop condition", nodes: withNode(withNode(base, &NodeConfig{ID: "b", Type: "agent", AgentName: "agentB", DependsOn: []string{"pick"}, Timeout: time.Minute}), &NodeConfig{ID: "again", Type: "loop", BodyNode: "b", Condition: "2 == 2"}), onExit: hashExit(), wantSame: true},
		{name: "a node type", nodes: withNode(base, &NodeConfig{ID: "b", Type: "workflow", DependsOn: []string{"pick"}}), onExit: hashExit(), wantSame: false},
		{name: "a node renamed", nodes: []*NodeConfig{base[0], base[1], {ID: "bee", Type: "agent", AgentName: "agentB", DependsOn: []string{"pick"}}, base[3], base[4], base[5]}, onExit: hashExit(), wantSame: false},
		{name: "a loop body", nodes: withNode(base, &NodeConfig{ID: "again", Type: "loop", BodyNode: "a", Condition: "1 == 1"}), onExit: hashExit(), wantSame: false},
		{name: "a map template", nodes: withNode(base, &NodeConfig{ID: "each", Type: "map", TemplateNode: "b"}), onExit: hashExit(), wantSame: false},
		{name: "a switch default", nodes: withNode(base, &NodeConfig{ID: "pick", Type: "switch", Cases: []SwitchCase{{Condition: "1 == 1", Node: "a"}}, DefaultCase: "a"}), onExit: hashExit(), wantSame: false},
		{name: "a decision escalate target", nodes: withNode(base, &NodeConfig{ID: "route", Type: "decision", Routes: map[string]string{"yes": "a", "no": "b"}, Escalate: "a", Default: "a", EscalationMode: "reroute"}), onExit: hashExit(), wantSame: false},
		{name: "a decision default", nodes: withNode(base, &NodeConfig{ID: "route", Type: "decision", Routes: map[string]string{"yes": "a", "no": "b"}, Escalate: "b", Default: "b", EscalationMode: "reroute"}), onExit: hashExit(), wantSame: false},
		{name: "a decision route target", nodes: withNode(base, &NodeConfig{ID: "route", Type: "decision", Routes: map[string]string{"yes": "b", "no": "b"}, Escalate: "b", Default: "a", EscalationMode: "reroute"}), onExit: hashExit(), wantSame: false},
		{name: "a decision route option", nodes: withNode(base, &NodeConfig{ID: "route", Type: "decision", Routes: map[string]string{"maybe": "a", "no": "b"}, Escalate: "b", Default: "a", EscalationMode: "reroute"}), onExit: hashExit(), wantSame: false},
		{name: "the escalation mode", nodes: withNode(base, &NodeConfig{ID: "route", Type: "decision", Routes: map[string]string{"yes": "a", "no": "b"}, Escalate: "b", Default: "a", EscalationMode: "handoff"}), onExit: hashExit(), wantSame: false},
		{name: "the on-failure handler", nodes: base, onExit: &ExitHandler{Always: "b", OnSuccess: "a", OnFailure: "a", OnCancel: "a"}, wantSame: false},
		{name: "the on-cancel handler", nodes: base, onExit: &ExitHandler{Always: "b", OnSuccess: "a", OnFailure: "b", OnCancel: "b"}, wantSame: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowDefinitionHash(&WorkflowConfig{Nodes: tc.nodes, OnExit: tc.onExit})
			if (got == reference) != tc.wantSame {
				t.Errorf("hash equal to the reference = %v, want %v", got == reference, tc.wantSame)
			}
		})
	}
}

func TestWorkflowDefinitionHash_FollowsTheSignalOfAWaitNode(t *testing.T) {
	waitGraph := func(signal string, timeout time.Duration) []*NodeConfig {
		return append(hashGraph(), &NodeConfig{ID: "pay", Type: NodeTypeWait, Signal: signal, Timeout: timeout, DependsOn: []string{"b"}})
	}
	reference := workflowDefinitionHash(&WorkflowConfig{Nodes: waitGraph("paid", 0), OnExit: hashExit()})
	tests := []struct {
		name     string
		nodes    []*NodeConfig
		wantSame bool
	}{
		{name: "a wait node signal renamed", nodes: waitGraph("settled", 0), wantSame: false},
		{name: "a wait node timeout changed", nodes: waitGraph("paid", time.Hour), wantSame: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowDefinitionHash(&WorkflowConfig{Nodes: tc.nodes, OnExit: hashExit()})
			if (got == reference) != tc.wantSame {
				t.Errorf("hash equal to the reference = %v, want %v", got == reference, tc.wantSame)
			}
		})
	}
}

func TestOpenPausedStore_NoDatabaseOpensNothingAndDoesNotFail(t *testing.T) {
	cfg := fourStepWorkflow()
	w := NewWorkflowInstance(cfg)
	reg := flagsOn("workflow_hil_pause_resume", features.KeyWorkflowCheckpointResume)

	if err := w.Init(t.Context(), nil, &stubServices{brk: &capturingBroker{}, features: reg}); err != nil {
		t.Fatalf("Init with no database configured: %v", err)
	}
	if w.pausedStore() != nil || w.checkpointStore() != nil {
		t.Error("a store is in use although no database is configured")
	}
}

func TestCheckpointResume_StateThatCannotBeContinuedEndsTheRun(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []*NodeConfig
		pending string
	}{
		{name: "a pending node no longer in the workflow", nodes: fourStepWorkflow().Nodes, pending: "ghost"},
		{name: "a pending loop with no iteration", nodes: []*NodeConfig{{ID: "again", Type: "loop", BodyNode: "body", Condition: "1 == 1", MaxIterations: 2}, {ID: "body", Type: "agent", AgentName: "loopWorker"}}, pending: "again"},
		{name: "a pending node of a type that cannot be sent again", nodes: []*NodeConfig{{ID: "pick", Type: "switch", Cases: []SwitchCase{{Condition: "1 == 1", Node: "a"}}}, {ID: "a", Type: "agent", AgentName: "agentA", DependsOn: []string{"pick"}}}, pending: "pick"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &WorkflowConfig{Name: "four-steps", Namespace: "test-ns", FailFast: true, Nodes: tc.nodes}
			awe := startCheckpointAWE(t, cfg, sharedStoreURL(t), checkpointFlag())
			const taskID = "019e0000-0000-7000-8000-00000c0e0c05"
			insertCheckpoint(t, awe, testCheckpoint{
				TaskID: taskID, ExecutionID: otherExecution, WorkflowName: "four-steps", DefinitionHash: awe.w.definitionHash,
				StateSnapshot: encodedPausedState(t, otherExecution, false, []string{tc.pending}),
				ExecContext:   encodedExecContext(t, taskID),
			})

			var acks ackCounter
			awe.deliver(t, taskID, &acks, true)

			waitFor(t, "the restored run to end", func() bool {
				return finalTaskOn(t, awe.brk.snapshot(), checkpointReplyTo) != nil
			})
			if final := finalTaskOn(t, awe.brk.snapshot(), checkpointReplyTo); final.Status.State == a2a.TaskStateCompleted {
				t.Errorf("a run that could not be continued ended %q, want it to fail", final.Status.State)
			}
		})
	}
}

func TestCheckpointResume_CancelledRunDispatchesNothing(t *testing.T) {
	awe := startCheckpointAWE(t, fourStepWorkflow(), sharedStoreURL(t), checkpointFlag())
	const taskID = "019e0000-0000-7000-8000-00000c0e0c06"
	state := NewExecutionState("four-steps", otherExecution)
	state.Cancelled = true
	state.PendingNodes["n1"] = true
	snapshot, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	insertCheckpoint(t, awe, testCheckpoint{
		TaskID: taskID, ExecutionID: otherExecution, WorkflowName: "four-steps", DefinitionHash: awe.w.definitionHash,
		StateSnapshot: string(snapshot), ExecContext: encodedExecContext(t, taskID),
	})

	var acks ackCounter
	awe.deliver(t, taskID, &acks, true)

	if got := awe.sendsTo("agent1"); got != 0 {
		t.Errorf("a cancelled run dispatched n1 %d times on restore, want 0", got)
	}
	if final := finalTaskOn(t, awe.brk.snapshot(), checkpointReplyTo); final == nil || final.Status.State == a2a.TaskStateCompleted {
		t.Errorf("a cancelled run restored ended with %+v, want a cancelled answer", final)
	}
}

func exportedFields(typ reflect.Type) []string {
	var names []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		switch {
		case f.Anonymous && f.Type.Kind() == reflect.Struct:
			names = append(names, exportedFields(f.Type)...)
		case f.IsExported():
			names = append(names, f.Name)
		}
	}
	slices.Sort(names)
	return names
}

func TestCheckpointExecContext_EveryStoredFieldIsReviewed(t *testing.T) {
	tests := []struct {
		name     string
		typ      reflect.Type
		stored   []string
		stripped []string
	}{
		{name: "user properties", typ: reflect.TypeFor[a2a.UserProperties](), stored: []string{
			"A2AExtensions", "A2AStatusTopic", "A2AUserConfig", "CallDepth", "Channel", "ClientID", "DelegatingAgentName",
			"EntrypointName", "EntrypointUUID", "GatewayCapabilities", "ReplyTo", "RootID", "RootKind", "RootNodeID",
			"Timestamp", "TraceID", "Trigger", "TriggerDeliveryID", "TriggerEvent", "TriggerRoute", "UserID", "UserUUID",
		}, stripped: []string{"AuthToken"}},
		{name: "identity", typ: reflect.TypeFor[auth.Identity](), stored: []string{
			"AuthMethod", "CredentialID", "PlatformUserID", "Roles", "Scopes", "UserID",
		}, stripped: []string{"AuthToken", "Claims"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reviewed := slices.Concat(tc.stored, tc.stripped)
			slices.Sort(reviewed)
			if got := exportedFields(tc.typ); !slices.Equal(got, reviewed) {
				t.Errorf("%s fields = %q, reviewed for checkpoints = %q; decide whether a checkpoint may store each new field",
					tc.typ, got, reviewed)
			}
		})
	}
}
