// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func signInEvent(kind string, extra map[string]any) *a2a.TaskStatusUpdateEvent {
	data := map[string]any{"type": kind}
	maps.Copy(data, extra)
	return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{Kind: "data", Data: data}}}}}
}

func signInResult(success bool) *a2a.TaskStatusUpdateEvent {
	return signInEvent("authentication_result", map[string]any{"success": success})
}

func askToSignIn(t *testing.T, a *Adapter, chat *fakeChat, taskID, tool string) string {
	t.Helper()
	a.OnStatusUpdate(t.Context(), taskID, signInEvent("authentication_required", map[string]any{"auth_uri": "https://accounts.example.com/authorize", "tool_display_name": tool}), nil)
	isLink := func(w fakeWrite) bool {
		return w.op == "create" && w.cardButtons == 1 && strings.Contains(w.fallback, "access "+tool+" on your behalf")
	}
	writes, _ := chat.waitFor(t, func(writes []fakeWrite, _ []string) bool {
		return slices.ContainsFunc(writes, isLink)
	})
	return writes[slices.IndexFunc(writes, isLink)].name
}

func waitForOutcome(t *testing.T, chat *fakeChat, link, outcome string) {
	t.Helper()
	chat.waitFor(t, func(writes []fakeWrite, _ []string) bool {
		return slices.ContainsFunc(writes, func(w fakeWrite) bool {
			return w.op == "update" && w.name == link && w.replacedCards && w.cardText == outcome && w.cardButtons == 0
		})
	})
}

func TestAuthorizationLinkPostedAsCardInDirectMessage(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-6"), dmMeta())

	askToSignIn(t, a, chat, tid("task-6"), "Calendar")
}

func TestSignInLinkIsReplacedByHowTheSignInEnded(t *testing.T) {
	for name, tc := range map[string]struct {
		success bool
		want    string
	}{
		"authorized":     {success: true, want: msgSignInAuthorized},
		"did not end ok": {success: false, want: msgSignInIncomplete},
	} {
		t.Run(name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			submitForTest(t, a, tid("task-7"), dmMeta())
			link := askToSignIn(t, a, chat, tid("task-7"), "Calendar")

			a.OnStatusUpdate(t.Context(), tid("task-7"), signInResult(tc.success), nil)

			waitForOutcome(t, chat, link, tc.want)
		})
	}
}

func TestEveryOutstandingSignInLinkIsReplaced(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-two"), dmMeta())
	calendar := askToSignIn(t, a, chat, tid("task-two"), "Calendar")
	drive := askToSignIn(t, a, chat, tid("task-two"), "Drive")

	a.OnStatusUpdate(t.Context(), tid("task-two"), signInResult(true), nil)

	waitForOutcome(t, chat, calendar, msgSignInAuthorized)
	waitForOutcome(t, chat, drive, msgSignInAuthorized)
}

func TestSignInOutcomeIsAppliedOnce(t *testing.T) {
	t.Run("no link outstanding", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		st := submitForTest(t, a, tid("task-8"), dmMeta())

		a.OnStatusUpdate(t.Context(), tid("task-8"), signInResult(true), nil)
		a.queueFor(tid("task-8"), st).Drain(5 * time.Second)

		writes, _ := chat.snapshot()
		for _, w := range writes {
			if w.replacedCards || w.cardText == msgSignInAuthorized || w.cardText == msgSignInIncomplete {
				t.Errorf("write = %+v, want no sign-in outcome written for a task with no sign-in link", w)
			}
		}
	})

	t.Run("a second outcome", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		st := submitForTest(t, a, tid("task-8b"), dmMeta())
		link := askToSignIn(t, a, chat, tid("task-8b"), "Calendar")

		a.OnStatusUpdate(t.Context(), tid("task-8b"), signInResult(true), nil)
		a.OnStatusUpdate(t.Context(), tid("task-8b"), signInResult(false), nil)
		a.queueFor(tid("task-8b"), st).Drain(5 * time.Second)

		if got := chat.updatesAttempted(link); got != 1 {
			t.Errorf("updates attempted on the link = %d, want 1", got)
		}
		waitForOutcome(t, chat, link, msgSignInAuthorized)
	})
}

func TestTaskEndingRetiresAnOutstandingSignInLink(t *testing.T) {
	for name, tc := range map[string]struct {
		end  func(t *testing.T, a *Adapter, taskID string)
		want string
	}{
		"completed": {want: msgSignInAuthorized, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnFinalResponse(t.Context(), taskID, completedTask("Done."), nil)
		}},
		"cancelled": {want: msgSignInIncomplete, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnFinalResponse(t.Context(), taskID, &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)
		}},
		"rejected": {want: msgSignInIncomplete, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnFinalResponse(t.Context(), taskID, &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateRejected}}, nil)
		}},
		"failed": {want: msgSignInIncomplete, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnFinalResponse(t.Context(), taskID, &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil)
		}},
		"error": {want: msgSignInIncomplete, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnError(t.Context(), taskID, &a2a.Error{Message: "boom"}, nil)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			submitForTest(t, a, tid("task-end"), dmMeta())
			link := askToSignIn(t, a, chat, tid("task-end"), "Calendar")

			tc.end(t, a, tid("task-end"))

			waitForOutcome(t, chat, link, tc.want)
		})
	}
}

func TestSignInLinkThatCouldNotBeReplacedIsRetriedWhenTheTaskEnds(t *testing.T) {
	chat := newFakeChat()
	db := store.TestDB(t)
	a := newTestAdapter(t, db, chat)
	submitForTest(t, a, tid("task-retry"), dmMeta())
	link := askToSignIn(t, a, chat, tid("task-retry"), "Calendar")
	waitForItem(t, db, tid("task-retry"), itemSignInMsgPrefix+link, link)
	chat.mu.Lock()
	chat.failCardAdd = &permanentError{err: errors.New("rejected")}
	chat.mu.Unlock()

	a.OnStatusUpdate(t.Context(), tid("task-retry"), signInResult(false), nil)
	waitUntilAttempted(t, chat, link, 1)
	a.queueFor(tid("task-retry"), a.tasks[tid("task-retry")]).Drain(5 * time.Second)
	waitForItem(t, db, tid("task-retry"), itemSignInMsgPrefix+link, link)

	chat.mu.Lock()
	chat.failCardAdd = nil
	chat.mu.Unlock()
	a.OnFinalResponse(t.Context(), tid("task-retry"), &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)

	waitForOutcome(t, chat, link, msgSignInIncomplete)
}

func waitUntilAttempted(t *testing.T, chat *fakeChat, name string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if chat.updatesAttempted(name) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no update was attempted on %s", name)
}

func TestSignInLinkMessagesAreRecordedUntilTheResult(t *testing.T) {
	chat := newFakeChat()
	db := store.TestDB(t)
	a := newTestAdapter(t, db, chat)
	submitForTest(t, a, tid("task-9"), dmMeta())
	calendar := askToSignIn(t, a, chat, tid("task-9"), "Calendar")
	drive := askToSignIn(t, a, chat, tid("task-9"), "Drive")
	waitForItem(t, db, tid("task-9"), itemSignInMsgPrefix+calendar, calendar)
	waitForItem(t, db, tid("task-9"), itemSignInMsgPrefix+drive, drive)

	a.OnStatusUpdate(t.Context(), tid("task-9"), signInResult(true), nil)

	waitForItemGone(t, db, tid("task-9"), itemSignInMsgPrefix+calendar)
	waitForItemGone(t, db, tid("task-9"), itemSignInMsgPrefix+drive)
}

func TestSignInResultReachesALinkAnotherReplicaPosted(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	submitForTest(t, replicaB, tid("task-x"), dmMeta())
	if err := replicaA.LoadTask(t.Context(), tid("task-x")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	link := askToSignIn(t, replicaA, chat, tid("task-x"), "Calendar")
	waitForItem(t, db, tid("task-x"), itemSignInMsgPrefix+link, link)

	replicaB.OnStatusUpdate(t.Context(), tid("task-x"), signInResult(true), nil)

	waitForOutcome(t, chat, link, msgSignInAuthorized)
}

func TestSignInLinksPostedByDifferentReplicasAreAllReplaced(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	submitForTest(t, replicaA, tid("task-y"), dmMeta())
	calendar := askToSignIn(t, replicaA, chat, tid("task-y"), "Calendar")
	waitForItem(t, db, tid("task-y"), itemSignInMsgPrefix+calendar, calendar)
	if err := replicaB.LoadTask(t.Context(), tid("task-y")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	drive := askToSignIn(t, replicaB, chat, tid("task-y"), "Drive")
	waitForItem(t, db, tid("task-y"), itemSignInMsgPrefix+drive, drive)

	replicaA.OnStatusUpdate(t.Context(), tid("task-y"), signInResult(true), nil)

	waitForOutcome(t, chat, calendar, msgSignInAuthorized)
	waitForOutcome(t, chat, drive, msgSignInAuthorized)
	waitForItemGone(t, db, tid("task-y"), itemSignInMsgPrefix+calendar)
	waitForItemGone(t, db, tid("task-y"), itemSignInMsgPrefix+drive)
}

func TestSignInLinkSettledByAnotherReplicaIsNotRewritten(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	submitForTest(t, replicaB, tid("task-z"), dmMeta())
	link := askToSignIn(t, replicaB, chat, tid("task-z"), "Calendar")
	waitForItem(t, db, tid("task-z"), itemSignInMsgPrefix+link, link)
	if err := replicaA.LoadTask(t.Context(), tid("task-z")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaA.OnStatusUpdate(t.Context(), tid("task-z"), signInResult(true), nil)
	waitForOutcome(t, chat, link, msgSignInAuthorized)
	waitForItemGone(t, db, tid("task-z"), itemSignInMsgPrefix+link)

	replicaB.OnFinalResponse(t.Context(), tid("task-z"), &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil)

	if got := chat.updatesAttempted(link); got != 1 {
		t.Errorf("updates attempted on the link = %d, want only the one that said it was authorized", got)
	}
}

func TestTaskEndingOnARecoveredReplicaRetiresEveryLink(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	submitForTest(t, replicaA, tid("task-w"), dmMeta())
	calendar := askToSignIn(t, replicaA, chat, tid("task-w"), "Calendar")
	drive := askToSignIn(t, replicaA, chat, tid("task-w"), "Drive")
	waitForItem(t, db, tid("task-w"), itemSignInMsgPrefix+calendar, calendar)
	waitForItem(t, db, tid("task-w"), itemSignInMsgPrefix+drive, drive)
	if err := replicaB.LoadTask(t.Context(), tid("task-w")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}

	replicaB.OnFinalResponse(t.Context(), tid("task-w"), &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)

	waitForOutcome(t, chat, calendar, msgSignInIncomplete)
	waitForOutcome(t, chat, drive, msgSignInIncomplete)
	waitForItemGone(t, db, tid("task-w"), itemSignInMsgPrefix+calendar)
	waitForItemGone(t, db, tid("task-w"), itemSignInMsgPrefix+drive)
}

func TestSignInOutcomeThatCouldNotBeWrittenIsNotChangedByTheTaskEnding(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-o"), dmMeta())
	link := askToSignIn(t, a, chat, tid("task-o"), "Calendar")
	chat.mu.Lock()
	chat.failCardAdd = &permanentError{err: errors.New("rejected")}
	chat.mu.Unlock()
	a.OnStatusUpdate(t.Context(), tid("task-o"), signInResult(false), nil)
	waitUntilAttempted(t, chat, link, 1)
	chat.mu.Lock()
	chat.failCardAdd = nil
	chat.mu.Unlock()

	a.OnFinalResponse(t.Context(), tid("task-o"), completedTask("Done."), nil)

	waitForOutcome(t, chat, link, msgSignInIncomplete)
}

func TestSignInLinkIsRetiredBeforeTheAnswerIsWritten(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-q"), dmMeta())
	link := askToSignIn(t, a, chat, tid("task-q"), "Calendar")

	a.OnFinalResponse(t.Context(), tid("task-q"), completedTask("The answer."), nil)

	writes, _ := chat.waitFor(t, func(writes []fakeWrite, _ []string) bool {
		return slices.ContainsFunc(writes, func(w fakeWrite) bool { return strings.Contains(w.text, "The answer.") })
	})
	outcome := slices.IndexFunc(writes, func(w fakeWrite) bool { return w.op == "update" && w.name == link && w.replacedCards })
	answer := slices.IndexFunc(writes, func(w fakeWrite) bool { return strings.Contains(w.text, "The answer.") })
	if outcome < 0 || outcome > answer {
		t.Errorf("link outcome written at %d, answer at %d, want the outcome first: %+v", outcome, answer, writes)
	}
}

func TestSignInMessagesIn(t *testing.T) {
	items := map[string]string{
		itemDestination:               `{"spaceName":"spaces/DM1"}`,
		itemStatusMsg:                 "spaces/DM1/messages/status",
		itemControlsMsg:               "spaces/DM1/messages/controls",
		itemPrompted:                  "",
		itemSignInMsgPrefix + "b-msg": "spaces/DM1/messages/b",
		itemSignInMsgPrefix + "a-msg": "spaces/DM1/messages/a",
		itemSignInMsgPrefix + "empty": "",
	}

	got := signInMessagesIn(items)

	if want := []string{"spaces/DM1/messages/a", "spaces/DM1/messages/b"}; !slices.Equal(got, want) {
		t.Errorf("signInMessagesIn = %q, want %q", got, want)
	}
}

func TestWriteAnchorsRecordsAndRemovesEachSignInLink(t *testing.T) {
	db := store.TestDB(t)
	a := newTestAdapter(t, db, newFakeChat())
	taskID := tid("task-anchors")
	if err := a.taskRegistry.Register(taskID, "agent", requesterEmail, entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}

	recorded := a.writeAnchors(taskID, queueAnchors{}, queueAnchors{SignIn: []string{"m/a", "m/b"}})
	if !slices.Equal(recorded.SignIn, []string{"m/a", "m/b"}) {
		t.Fatalf("recorded = %q, want both links", recorded.SignIn)
	}
	recorded = a.writeAnchors(taskID, recorded, queueAnchors{SignIn: []string{"m/b"}})
	items, err := a.items.List(t.Context(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got := signInMessagesIn(items); !slices.Equal(got, []string{"m/b"}) {
		t.Errorf("recorded items = %q, want only m/b", got)
	}
	recorded = a.writeAnchors(taskID, recorded, queueAnchors{})
	if recorded.SignIn != nil {
		t.Errorf("recorded = %#v, want no links recorded as nil", recorded.SignIn)
	}
}
