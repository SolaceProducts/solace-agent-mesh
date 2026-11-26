// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type heldEvent struct {
	ctx  context.Context
	d    *delivery.Delivery
	acks atomic.Int32
}

func receiveEvent(t *testing.T) *heldEvent {
	e := &heldEvent{}
	e.ctx, e.d = delivery.Attach(t.Context(), "m1", false, func() { e.acks.Add(1) })
	return e
}

func (e *heldEvent) waitAcked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.acks.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the event was never acknowledged")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func requireTaskRecorded(t *testing.T, a *Adapter, taskID string, want bool) {
	t.Helper()
	if running, known := a.taskRegistry.RunState(taskID); !known || running != want {
		t.Errorf("task record present = %v (known %v), want %v", running, known, want)
	}
	items, err := a.items.List(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := items[itemDestination]; found != want {
		t.Errorf("destination item present = %v, want %v", found, want)
	}
}

func TestQuestionEventAcknowledgedOnceTheCardIsPosted(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	q.Drain(5 * time.Second)
	release := holdWrites(chat)

	ev := receiveEvent(t)
	req := askRequest()
	req.Type = "user_input_request"
	a.OnStatusUpdate(ev.ctx, tid("task-1"), &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{
		State:   a2a.TaskStateInputNeeded,
		Message: &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(req)}},
	}}, nil)
	ev.d.Release()
	time.Sleep(50 * time.Millisecond)
	if ev.acks.Load() != 0 {
		t.Fatal("the question's event was acknowledged before its card was posted")
	}

	close(release)
	ev.waitAcked(t)
}

func TestFinalEventAcknowledgedOnceTheAnswerIsWritten(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.lingerTimeout = time.Minute
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	release := holdWrites(chat)

	ev := receiveEvent(t)
	a.OnFinalResponse(ev.ctx, tid("task-1"), completedTask("The final answer"), nil)
	ev.d.Release()
	if ev.acks.Load() != 0 {
		t.Fatal("the final event was acknowledged before its answer was written")
	}
	requireTaskRecorded(t, a, tid("task-1"), true)

	a.OnStatusUpdate(t.Context(), tid("task-1"), textEvent("late text"), nil)
	close(release)
	ev.waitAcked(t)
	requireTaskRecorded(t, a, tid("task-1"), false)
	if _, texts := chat.snapshot(); !anyTextContains(texts, "The final answer") || anyTextContains(texts, "late text") {
		t.Errorf("messages = %q, want the final answer and nothing of a status update after it", texts)
	}
}

func TestStopLeavesAnUnwrittenFinalAnswerToTheNextGWE(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	a := newTestAdapter(t, db, chat)
	a.lingerTimeout = time.Minute
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	holdWrites(chat)

	ev := receiveEvent(t)
	a.OnFinalResponse(ev.ctx, tid("task-1"), completedTask("The final answer"), nil)
	ev.d.Release()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_ = a.Stop(ctx)

	if ev.acks.Load() != 0 {
		t.Fatal("the final event was acknowledged although its answer was never written")
	}
	requireTaskRecorded(t, a, tid("task-1"), true)

	nextChat := newFakeChat()
	next := newTestAdapter(t, db, nextChat)
	if err := next.LoadTask(t.Context(), tid("task-1")); err != nil {
		t.Fatal(err)
	}
	again := receiveEvent(t)
	next.OnFinalResponse(again.ctx, tid("task-1"), completedTask("The final answer"), nil)
	again.d.Release()
	again.waitAcked(t)
	if _, texts := nextChat.snapshot(); !anyTextContains(texts, "The final answer") {
		t.Errorf("next GWE messages = %q, want the final answer", texts)
	}
	requireTaskRecorded(t, next, tid("task-1"), false)
}

func TestSavedFilesReachAReplicaThatLoadsTheTask(t *testing.T) {
	db := store.TestDB(t)
	a := newTestAdapter(t, db, newFakeChat())
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	save := func(name string, version int, tags ...string) a2a.Part {
		data := map[string]any{"type": string(a2a.SignalArtifactSaved), "filename": name, "mime_type": "text/csv", "version": version}
		if len(tags) > 0 {
			data["tags"] = tags
		}
		return a2a.NewDataPart(data)
	}
	a.OnStatusUpdate(t.Context(), tid("task-1"), &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{save("b.csv", 1), save("a.csv", 1)},
	}}}, nil)
	a.OnStatusUpdate(t.Context(), tid("task-1"), &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{save("b.csv", 2), save("c.csv", 1, a2a.ArtifactTagWorking)},
	}}}, nil)
	if got := candidateVersions(driveCandidates(st.savedArtifacts(), []string{"a.csv", "b.csv", "c.csv"})); len(got) != 2 {
		t.Fatalf("this replica's candidates = %v", got)
	}

	other := newTestAdapter(t, db, newFakeChat())
	loaded, _, err := other.loadTask(t.Context(), tid("task-1"))
	if err != nil || loaded == nil {
		t.Fatalf("loadTask = %v, %v", loaded, err)
	}
	got := candidateVersions(driveCandidates(loaded.savedArtifacts(), []string{"a.csv", "b.csv", "c.csv"}))
	if want := []string{"b.csv@2", "a.csv@1"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("loaded candidates = %v, want %v: latest save of each, in the order first saved, no working file", got, want)
	}
}

func TestFinalEventAcknowledgedWhenItsWritesEnd(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reject bool
	}{{name: "written"}, {name: "refused by chat", reject: true}} {
		t.Run(tc.name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			submitForTest(t, a, tid("task-1"), dmMeta())
			chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
			if tc.reject {
				rejectNextWrite(chat)
			}

			ev := receiveEvent(t)
			a.OnFinalResponse(ev.ctx, tid("task-1"), completedTask("The final answer"), nil)
			ev.d.Release()
			waitTerminalWritten(t, a)
			if ev.acks.Load() != 1 {
				t.Fatalf("acks = %d, want the event acknowledged once its writes ended", ev.acks.Load())
			}
			requireTaskRecorded(t, a, tid("task-1"), false)
		})
	}
}

func TestErrorEventAcknowledgedOnceItsNoticeIsWritten(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.lingerTimeout = time.Minute
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	release := holdWrites(chat)

	ev := receiveEvent(t)
	a.OnError(ev.ctx, tid("task-1"), &a2a.Error{Code: -32000, Message: "boom"}, nil)
	ev.d.Release()
	if ev.acks.Load() != 0 {
		t.Fatal("the error event was acknowledged before its notice was written")
	}
	close(release)
	ev.waitAcked(t)
	requireTaskRecorded(t, a, tid("task-1"), false)
}

func TestSignInEventAcknowledgedOnceTheLinkIsPosted(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	q.Drain(5 * time.Second)
	release := holdWrites(chat)

	ev := receiveEvent(t)
	a.promptForAuthorization(ev.ctx, tid("task-1"), st, q, &a2a.AuthenticationRequiredData{AuthURI: httpsAuthURI})
	ev.d.Release()
	if ev.acks.Load() != 0 {
		t.Fatal("the sign-in event was acknowledged before its link was posted")
	}
	close(release)
	ev.waitAcked(t)
}

func TestRepeatedTerminalEventWaitsForTheFirst(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.lingerTimeout = time.Minute
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	release := holdWrites(chat)

	first := receiveEvent(t)
	a.OnFinalResponse(first.ctx, tid("task-1"), completedTask("The final answer"), nil)
	first.d.Release()
	again := receiveEvent(t)
	a.OnFinalResponse(again.ctx, tid("task-1"), completedTask("The final answer"), nil)
	again.d.Release()
	if again.acks.Load() != 0 {
		t.Fatal("the repeated terminal event was acknowledged while the first was still written")
	}

	close(release)
	first.waitAcked(t)
	again.waitAcked(t)
}

func TestSubmittingReplicaHandsOverFilesItDidNotSeeSaved(t *testing.T) {
	db := store.TestDB(t)
	submitter := newTestAdapter(t, db, newFakeChat())
	st := submitForTest(t, submitter, tid("task-1"), dmMeta())
	receiver := newTestAdapter(t, db, newFakeChat())
	if err := receiver.LoadTask(t.Context(), tid("task-1")); err != nil {
		t.Fatal(err)
	}
	receiver.OnStatusUpdate(t.Context(), tid("task-1"), &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": string(a2a.SignalArtifactSaved), "filename": "a.csv", "mime_type": "text/csv", "version": 1})},
	}}}, nil)

	submitter.mergeRecordedSaves(t.Context(), tid("task-1"), st)
	if got := candidateVersions(driveCandidates(st.savedArtifacts(), []string{"a.csv"})); len(got) != 1 || got[0] != "a.csv@1" {
		t.Errorf("candidates = %v, want the file the other replica saw saved", got)
	}
}

func TestDriveHandOffHoldsItsEvent(t *testing.T) {
	t.Run("acknowledged once the links are posted", func(t *testing.T) {
		r, st, taskID := newDrainRig(t)
		q := r.a.queueFor(taskID, st)
		q.Replace("The final answer")
		ev := receiveEvent(t)
		r.a.drainTerminal(taskID, st, q, r.work("a.csv"), r.a.holdTaskEnd(ev.ctx, taskID))
		ev.d.Release()

		waitForResultMessage(t, r.chat)
		ev.waitAcked(t)
		requireTaskRecorded(t, r.a, taskID, false)
	})
	t.Run("kept for the next GWE when Stop abandons it", func(t *testing.T) {
		r, st, taskID := newDrainRig(t)
		r.drive.upload = func(ctx context.Context, _, _ string, _ map[string]string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}
		q := r.a.queueFor(taskID, st)
		q.Replace("The final answer")
		ev := receiveEvent(t)
		r.a.drainTerminal(taskID, st, q, r.work("a.csv"), r.a.holdTaskEnd(ev.ctx, taskID))
		ev.d.Release()
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		_ = r.a.Stop(ctx)

		if ev.acks.Load() != 0 {
			t.Fatal("the event was acknowledged although Stop abandoned its files")
		}
		requireTaskRecorded(t, r.a, taskID, true)
		if r.logs.count(slog.LevelWarn, logFilesNotHandedOff) != 1 || r.logs.count(slog.LevelError, logFilesNotHandedOff) != 0 {
			t.Error("a hand-off left to the next GWE is logged at WARN, not ERROR")
		}
	})
}

func TestHeldTerminalEventArrivingDuringStopIsLeftToTheNextGWE(t *testing.T) {
	t.Run("answer", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		submitForTest(t, a, tid("task-1"), dmMeta())
		chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
		holdWrites(chat)
		a.awaitLingeringReplies(t.Context())

		ev := receiveEvent(t)
		a.OnFinalResponse(ev.ctx, tid("task-1"), completedTask("The final answer"), nil)
		ev.d.Release()
		if ev.acks.Load() != 0 {
			t.Fatal("the event was acknowledged although its answer was never written")
		}
		requireTaskRecorded(t, a, tid("task-1"), true)
	})
	t.Run("drive hand-off", func(t *testing.T) {
		r, st, taskID := newDrainRig(t)
		r.a.awaitLingeringReplies(t.Context())
		q := r.a.queueFor(taskID, st)
		q.Replace("The final answer")
		ev := receiveEvent(t)
		r.a.drainTerminal(taskID, st, q, r.work("a.csv"), r.a.holdTaskEnd(ev.ctx, taskID))
		ev.d.Release()
		if ev.acks.Load() != 0 {
			t.Fatal("the event was acknowledged although its files were never handed over")
		}
		requireTaskRecorded(t, r.a, taskID, true)
	})
}

func TestFinalAnswerNamesFilesAnotherReplicaSawSaved(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	submitter := newTestAdapter(t, db, chat)
	submitForTest(t, submitter, tid("task-1"), dmMeta())
	receiver := newTestAdapter(t, db, newFakeChat())
	if err := receiver.LoadTask(t.Context(), tid("task-1")); err != nil {
		t.Fatal(err)
	}
	receiver.OnStatusUpdate(t.Context(), tid("task-1"), savedFilesEvent("report.csv"), nil)

	submitter.OnFinalResponse(t.Context(), tid("task-1"), completedTask("Done."), nil)
	waitTerminalWritten(t, submitter)
	if _, texts := chat.snapshot(); !anyTextContains(texts, "report.csv") {
		t.Errorf("messages = %q, want the file the other replica saw saved named", texts)
	}
}

func TestSavedFilesPastTheCapAreKeptInMemoryOnly(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	names := make([]string, maxRecordedSaves+1)
	for i := range names {
		names[i] = fmt.Sprintf("f%02d.csv", i)
	}
	a.OnStatusUpdate(t.Context(), tid("task-1"), savedFilesEvent(names...), nil)
	a.OnStatusUpdate(t.Context(), tid("task-1"), savedFilesEvent(names[0]), nil)

	items, err := a.items.List(t.Context(), tid("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	recorded := 0
	for item := range items {
		if strings.HasPrefix(item, itemSavedFilePrefix) {
			recorded++
		}
	}
	if _, last := items[itemSavedFilePrefix+names[maxRecordedSaves]]; recorded != maxRecordedSaves || last {
		t.Errorf("recorded = %d (the one past the cap recorded: %v), want %d", recorded, last, maxRecordedSaves)
	}
	if n := len(st.savedArtifacts()); n != len(names)+1 {
		t.Errorf("saves kept in memory = %d, want %d", n, len(names)+1)
	}
}

func TestSaveAfterALoadContinuesTheOrder(t *testing.T) {
	db := store.TestDB(t)
	a := newTestAdapter(t, db, newFakeChat())
	submitForTest(t, a, tid("task-1"), dmMeta())
	a.OnStatusUpdate(t.Context(), tid("task-1"), savedFilesEvent("a.csv", "b.csv"), nil)

	other := newTestAdapter(t, db, newFakeChat())
	loaded, _, err := other.loadTask(t.Context(), tid("task-1"))
	if err != nil || loaded == nil {
		t.Fatalf("loadTask = %v, %v", loaded, err)
	}
	if got := loaded.recordSaved([]entrypoint.SavedArtifact{{Filename: "c.csv"}}); len(got) != 1 || got[0].Seq != 3 {
		t.Errorf("recorded = %+v, want c.csv third", got)
	}
}

func TestRefusedQuestionEventIsAcknowledged(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	attachCore(t, a)
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	q.Drain(5 * time.Second)
	chat.mu.Lock()
	chat.failures = []error{nil, &permanentError{err: fmt.Errorf("400 invalid card")}}
	chat.mu.Unlock()

	ev := receiveEvent(t)
	a.promptForInput(ev.ctx, tid("task-1"), st, q, askRequest())
	ev.d.Release()
	ev.waitAcked(t)
}

func savedFilesEvent(names ...string) *a2a.TaskStatusUpdateEvent {
	parts := make([]a2a.Part, len(names))
	for i, name := range names {
		parts[i] = a2a.NewDataPart(map[string]any{"type": string(a2a.SignalArtifactSaved), "filename": name, "mime_type": "text/csv", "version": 1})
	}
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: parts}}}
}
