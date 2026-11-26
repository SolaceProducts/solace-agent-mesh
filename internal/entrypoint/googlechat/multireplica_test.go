// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestRecoveringReplicaAdoptsStatusMessagePostedAfterIt(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)

	st := &taskState{meta: dmMeta()}
	if !replicaA.recordTask(t.Context(), tid("task-r"), requesterEmail, st) {
		t.Fatal("recordTask did not record the task")
	}
	if err := replicaB.LoadTask(t.Context(), tid("task-r")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaB.OnStatusUpdate(t.Context(), tid("task-r"), progressEvent("Looking it up"), nil)

	time.Sleep(3 * statusAdoptPoll)
	st.queue.Placeholder(replicaA.cfg.InitialStatusMessage)

	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool {
		return anyTextContains(texts, "Looking it up") && st.snapshot().StatusMessage != ""
	})
	if len(texts) != 1 {
		t.Errorf("messages = %q, want the submitter's status message reused, not a second one", texts)
	}
}

func TestSignInResultOnAnotherReplicaReplacesTheLink(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	submitForTest(t, replicaA, tid("task-s"), dmMeta())
	link := askToSignIn(t, replicaA, chat, tid("task-s"), "Calendar")
	waitForItem(t, db, tid("task-s"), itemSignInMsgPrefix+link, link)
	waitForItem(t, db, tid("task-s"), itemPrompted, "")

	if err := replicaB.LoadTask(t.Context(), tid("task-s")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaB.OnStatusUpdate(t.Context(), tid("task-s"), signInResult(true), nil)

	waitForOutcome(t, chat, link, msgSignInAuthorized)
	waitForItemGone(t, db, tid("task-s"), itemSignInMsgPrefix+link)
}

func TestIdleTaskStateIsReleasedAndRecoveredLater(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	a := newTestAdapter(t, db, chat)
	submitForTest(t, a, tid("task-idle"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	waitForPersistedStatus(t, a, tid("task-idle"), writes[0].name)
	submitForTest(t, a, tid("task-busy"), dmMeta())

	later := time.Now().Add(taskIdleRelease)
	a.tasks[tid("task-busy")].touch(later)
	a.releaseIdleTasks(later)

	if _, held := a.tasks[tid("task-idle")]; held {
		t.Error("task-idle is still held after going idle")
	}
	if _, held := a.tasks[tid("task-busy")]; !held {
		t.Error("task-busy was released while still active")
	}

	if err := a.LoadTask(t.Context(), tid("task-idle")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	a.OnFinalResponse(t.Context(), tid("task-idle"), completedTask("Answered after release."), nil)
	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "Answered after release.") })
}

func TestRecoveredTaskAfterPromptAnswersBelowIt(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	st := submitForTest(t, replicaA, tid("task-p"), dmMeta())
	st.queue.Prompt("❓ Which day?", "Which day?", nil)
	waitForItem(t, db, tid("task-p"), itemPrompted, "")

	replicaB := newTestAdapter(t, db, chat)
	if err := replicaB.LoadTask(t.Context(), tid("task-p")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaB.OnFinalResponse(t.Context(), tid("task-p"), completedTask("Booked for Thursday."), nil)

	_, texts := chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "Booked for Thursday.") })
	if len(texts) != 3 || texts[0] != "❓ Which day?" || texts[2] != "Booked for Thursday." {
		t.Errorf("messages = %q, want the lead, the prompt card, then the answer below it", texts)
	}
}
