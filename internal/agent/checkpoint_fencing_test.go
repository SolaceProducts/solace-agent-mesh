// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func publishedOn(brk *mockBroker, topic string) []publishedMsg {
	var out []publishedMsg
	for _, msg := range brk.getPublished() {
		if msg.topic == topic {
			out = append(out, msg)
		}
	}
	return out
}

func peerCancelsSent(brk *mockBroker) int {
	n := 0
	for _, msg := range brk.getPublished() {
		if strings.Contains(string(msg.payload), "tasks/cancel") {
			n++
		}
	}
	return n
}

func pauseOnPeer(t *testing.T, replica *Agent, store session.Store) *TaskExecutionState {
	t.Helper()
	taskID, subTaskID := idgen.New(), idgen.New()
	state := pausedOnPeerCall(t, store, taskID, subTaskID)
	if err := replica.checkpoints.save(t.Context(), state); err != nil {
		t.Fatalf("save: %v", err)
	}
	replica.pausedTasks.Store(state)
	replica.peerRouter.Register(subTaskID, "status", taskID, replica.cfg.Name, "peer", "tc-1", "", "", "", time.Hour)
	replica.seqGen.Next(taskID)
	return state
}

func TestAFrozenOwnerThatWakesAfterATakeoverStandsDown(t *testing.T) {
	store, db := newSQLiteCheckpointStore(t)
	logs := &syncLogBuffer{}
	frozen, _, frozenBrk := replicaOf(t, store, "frozen", capturingLogs(logs))
	successor, successorLLM, successorBrk := replicaOf(t, store, "successor")

	state := pauseOnPeer(t, frozen, store)
	var turnStopped atomic.Bool
	frozen.activeTasks[state.TaskID] = &activeTaskEntry{
		cancel:      func() { turnStopped.Store(true) },
		userProps:   a2a.UserProperties{UserID: "u", ClientID: "c", TraceID: "trace-frozen"},
		publishOnce: &sync.Once{},
	}
	pc := state.PendingCalls["tc-1"]
	frozen.checkpoints.recordResult(t.Context(), state.TaskID, pc, tool.OK("the peer answered"))
	silenceOwner(t, db, state.TaskID, checkpointTakeoverAfter+time.Second)

	successor.resumeAbandonedCheckpoints(t.Context(), time.Now())
	assertResumedToCompletion(t, successor, successorLLM, successorBrk, state.TaskID, "the peer answered")
	if !waitUntil(func() bool {
		cp, err := store.GetCheckpoint(t.Context(), state.TaskID)
		return err == nil && cp == nil
	}) {
		t.Fatal("the successor finished the task but left its row")
	}

	err := frozen.checkpoints.save(t.Context(), state)
	if !errors.Is(err, errTaskLost) {
		t.Fatalf("the late save = %v, want it refused as a lost task", err)
	}
	if cp, _ := store.GetCheckpoint(t.Context(), state.TaskID); cp != nil {
		t.Fatal("the late save recreated a task another AWE already answered")
	}

	frozen.renewOwnedLeases(t.Context(), time.Now())

	if frozen.pausedTasks.Load(state.TaskID) != nil {
		t.Error("the frozen owner still holds the task after standing down")
	}
	if !turnStopped.Load() {
		t.Error("the frozen owner left the task's running turn going after standing down")
	}
	withheld := logs.records("stopping a running turn of a task another process now holds; withholding its response")
	if len(withheld) != 1 || withheld[0]["level"] != "DEBUG" || withheld[0]["taskID"] != state.TaskID || withheld[0]["traceID"] != "trace-frozen" {
		t.Errorf("stand-down log lines = %v, want one Debug line for task %q and trace %q", withheld, state.TaskID, "trace-frozen")
	}
	if n := frozen.peerRouter.ReleaseTask(state.TaskID); n != 0 {
		t.Errorf("the frozen owner kept %d peer waits for a task it lost", n)
	}
	if n := peerCancelsSent(frozenBrk); n != 0 {
		t.Errorf("the frozen owner sent %d peer cancels; the peer's work belongs to the successor", n)
	}
	if next := frozen.seqGen.Next(state.TaskID); next != 1 {
		t.Errorf("the frozen owner kept the task's event numbering (next = %d)", next)
	}
	if got := publishedOn(frozenBrk, resumedReplyTopic); len(got) != 0 {
		t.Errorf("the frozen owner published %d answers for a task it lost", len(got))
	}
}

type correlationsRefusingStore struct {
	session.Store
}

func (correlationsRefusingStore) SaveCheckpoint(context.Context, *session.TaskCheckpoint) error {
	return session.ErrNotOwner
}

func (correlationsRefusingStore) UpdateCheckpoint(context.Context, *session.TaskCheckpoint) error {
	return session.ErrNotOwner
}

func TestASaveWhoseCorrelationsAreRefusedBecauseAnotherOwnerHoldsTheTaskIsRefusedAsALostTask(t *testing.T) {
	replica, _, _ := replicaOf(t, correlationsRefusingStore{Store: session.NewMemoryStore()}, "frozen")
	state := pausedOnPeerCall(t, replica.checkpoints.store, idgen.New(), idgen.New())

	err := replica.checkpoints.save(t.Context(), state)

	if !errors.Is(err, errTaskLost) {
		t.Errorf("save = %v, want it refused as a lost task", err)
	}
}

type failingRenewals struct {
	session.Store
}

func (failingRenewals) RenewLeases(context.Context, string, []string, time.Time) (int, error) {
	return 0, errors.New("database unreachable")
}

func TestAnAWEWhoseOwnRenewalsFailDoesNotTakeOver(t *testing.T) {
	store, db := newSQLiteCheckpointStore(t)
	owner, _, _ := replicaOf(t, store, "owner")
	adopter, adopterLLM, _ := replicaOf(t, store, "adopter", func(a *Agent) {
		a.checkpoints.takeoverAfter = 100 * time.Millisecond
	})

	state := pauseOnPeer(t, owner, store)
	owner.checkpoints.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-1"], tool.OK("answer"))
	silenceOwner(t, db, state.TaskID, time.Hour)

	working := adopter.checkpoints.store
	adopter.checkpoints.store = failingRenewals{Store: working}
	adopter.renewOwnedLeases(t.Context(), time.Now())
	adopter.checkpoints.store = working

	adopter.resumeAbandonedCheckpoints(t.Context(), time.Now())
	if brieflyBecomes(func() bool { return len(adopterLLM.getCalls()) > 0 }) {
		t.Fatal("an AWE whose last renewal failed took over a task")
	}

	adopter.renewOwnedLeases(t.Context(), time.Now())
	adopter.resumeAbandonedCheckpoints(t.Context(), time.Now())
	if brieflyBecomes(func() bool { return len(adopterLLM.getCalls()) > 0 }) {
		t.Fatal("an AWE took over a task one good renewal after an outage, before the takeover window")
	}

	time.Sleep(2 * adopter.checkpoints.takeoverAfter)
	adopter.renewOwnedLeases(t.Context(), time.Now())
	adopter.resumeAbandonedCheckpoints(t.Context(), time.Now())
	if !waitUntil(func() bool { return len(adopterLLM.getCalls()) > 0 }) {
		t.Fatal("an AWE whose renewals reached the database for the takeover window never took the task over")
	}
}

func markAnsweringAndDie(t *testing.T, owner *Agent, store session.Store) *TaskExecutionState {
	t.Helper()
	state := pauseOnPeer(t, owner, store)
	resp := buildCancelledResponse(state.TaskID, state.ContextID, owner.cfg.Name, state.usageSnapshot(), &state.usageFacts)
	final := owner.finalMessageFor(resp, state, owner.logger)
	if final == nil {
		t.Fatal("no final message to store")
	}
	if !owner.checkpoints.markAnswering(t.Context(), state, final) {
		t.Fatal("the owner could not mark its own row answering")
	}
	owner.pausedTasks.Delete(state.TaskID)
	return state
}

func TestTakeoverOfAnAnsweringRowPublishesTheStoredAnswerOnce(t *testing.T) {
	store, db := newSQLiteCheckpointStore(t)
	owner, _, ownerBrk := replicaOf(t, store, "owner")
	successor, successorLLM, successorBrk := replicaOf(t, store, "successor")

	state := markAnsweringAndDie(t, owner, store)
	stored, err := store.GetCheckpoint(t.Context(), state.TaskID)
	if err != nil || stored == nil || stored.Final == nil {
		t.Fatalf("the answering row was not written: %v, %v", stored, err)
	}
	if got := publishedOn(ownerBrk, resumedReplyTopic); len(got) != 0 {
		t.Fatal("setup: the owner published before dying, so this proves nothing")
	}

	silenceOwner(t, db, state.TaskID, checkpointTakeoverAfter+time.Second)
	successor.resumeAbandonedCheckpoints(t.Context(), time.Now())
	successor.resumeAbandonedCheckpoints(t.Context(), time.Now())

	got := publishedOn(successorBrk, resumedReplyTopic)
	if len(got) != 1 {
		t.Fatalf("the successor published %d answers, want exactly the stored one", len(got))
	}
	if !bytes.Equal(got[0].payload, stored.Final.Payload) || !maps.Equal(got[0].userProps, stored.Final.UserProperties) {
		t.Errorf("published %s %v, want the stored message %s %v", got[0].payload, got[0].userProps, stored.Final.Payload, stored.Final.UserProperties)
	}
	if len(successorLLM.getCalls()) != 0 {
		t.Error("the successor ran the task again instead of publishing its stored answer")
	}
	if cp, _ := store.GetCheckpoint(t.Context(), state.TaskID); cp != nil {
		t.Error("the successor published the stored answer and left the row, so the next takeover publishes it again")
	}
}

func TestTakeoverOfAnAnsweringRunBasedTaskRemovesItsSession(t *testing.T) {
	store, db := newSQLiteCheckpointStore(t)
	owner, _, _ := replicaOf(t, store, "owner")
	successor, _, successorBrk := replicaOf(t, store, "successor")
	ctx := t.Context()

	state := pauseOnPeer(t, owner, store)
	state.IsRunBased = true
	if err := owner.checkpoints.save(ctx, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	if sess, err := store.Get(ctx, owner.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID); err != nil || sess == nil {
		t.Fatalf("setup: the run-based session is missing: %v, %v", sess, err)
	}
	resp := buildCancelledResponse(state.TaskID, state.ContextID, owner.cfg.Name, state.usageSnapshot(), &state.usageFacts)
	if !owner.checkpoints.markAnswering(ctx, state, owner.finalMessageFor(resp, state, owner.logger)) {
		t.Fatal("the owner could not mark its own row answering")
	}
	owner.pausedTasks.Delete(state.TaskID)

	silenceOwner(t, db, state.TaskID, checkpointTakeoverAfter+time.Second)
	successor.resumeAbandonedCheckpoints(ctx, time.Now())

	if got := publishedOn(successorBrk, resumedReplyTopic); len(got) != 1 {
		t.Fatalf("the successor published %d answers, want the stored one", len(got))
	}
	if sess, err := store.Get(ctx, owner.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID); err != nil || sess != nil {
		t.Errorf("run-based session after the adopted answer = %v (err %v), want it removed", sess, err)
	}
}

func TestCancelOfATaskBeingAnsweredStandsDown(t *testing.T) {
	store, _ := newSQLiteCheckpointStore(t)
	owner, _, _ := replicaOf(t, store, "owner")
	other, _, otherBrk := replicaOf(t, store, "other")

	state := markAnsweringAndDie(t, owner, store)

	if other.cancelCheckpointedTask(state.TaskID) {
		t.Error("the cancel took a row being answered")
	}
	if got := publishedOn(otherBrk, resumedReplyTopic); len(got) != 0 {
		t.Errorf("the cancel published %d responses for a task being answered", len(got))
	}
	if cp, _ := store.GetCheckpoint(t.Context(), state.TaskID); cp == nil || cp.Final == nil {
		t.Error("the cancel removed the stored answer; a takeover can no longer publish it")
	}
}

func TestHeldCancelAnswersOnlyWhenItRemovedItsOwnRow(t *testing.T) {
	store, _ := newSQLiteCheckpointStore(t)
	owner, _, ownerBrk := replicaOf(t, store, "owner")

	mine := pauseOnPeer(t, owner, store)
	owner.cancelPausedTask(mine, cancelFromElsewhere)
	if got := publishedOn(ownerBrk, resumedReplyTopic); len(got) != 1 {
		t.Fatalf("a cancel of a task this AWE holds published %d responses, want 1", len(got))
	}
	if cp, _ := store.GetCheckpoint(t.Context(), mine.TaskID); cp != nil {
		t.Error("the cancel left the row behind")
	}

	taken := pauseOnPeer(t, owner, store)
	owner.checkpoints.recordResult(t.Context(), taken.TaskID, taken.PendingCalls["tc-1"], tool.OK("answer"))
	if cp, err := takeOver(t, store, taken.TaskID, "successor"); err != nil || cp == nil {
		t.Fatalf("setup: the successor did not take the task over: %v, %v", cp, err)
	}
	final := &session.FinalMessage{Topic: resumedReplyTopic, Payload: []byte(`{"answer":"from the successor"}`)}
	if marked, err := store.MarkAnswering(t.Context(), taken.TaskID, "successor", final, time.Now().Add(time.Minute)); err != nil || !marked {
		t.Fatalf("setup: the successor could not mark the task answering: %v, %v", marked, err)
	}

	owner.cancelPausedTask(taken, cancelFromElsewhere)

	if got := publishedOn(ownerBrk, resumedReplyTopic); len(got) != 1 {
		t.Errorf("a cancel of a task another AWE holds and is answering published here (%d responses)", len(got))
	}
	if owner.pausedTasks.Load(taken.TaskID) != nil {
		t.Error("the cancelled task is still held here")
	}
	if cp, _ := store.GetCheckpoint(t.Context(), taken.TaskID); cp == nil || cp.Final == nil {
		t.Error("the cancel removed the successor's answering row")
	}
}
