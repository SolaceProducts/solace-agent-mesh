// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type signInItemsRejected struct {
	taskItemStore
}

func (s signInItemsRejected) Put(ctx context.Context, taskID, item, value string) error {
	if len(item) >= len(itemSignInMsgPrefix) && item[:len(itemSignInMsgPrefix)] == itemSignInMsgPrefix {
		return errItemsUnreachable
	}
	return s.taskItemStore.Put(ctx, taskID, item, value)
}

func setCardWrites(chat *fakeChat, err error) {
	chat.mu.Lock()
	defer chat.mu.Unlock()
	chat.failCardAdd = err
}

func TestSignInLinkWhoseItemCouldNotBeRecordedIsStillReplaced(t *testing.T) {
	for name, tc := range map[string]struct {
		end  func(t *testing.T, a *Adapter, taskID string)
		want string
	}{
		"by the outcome": {want: msgSignInAuthorized, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnStatusUpdate(t.Context(), taskID, signInResult(true), nil)
		}},
		"by the task ending": {want: msgSignInIncomplete, end: func(t *testing.T, a *Adapter, taskID string) {
			a.OnFinalResponse(t.Context(), taskID, &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			a.items = signInItemsRejected{taskItemStore: a.items}
			submitForTest(t, a, tid("task-rej"), dmMeta())
			link := askToSignIn(t, a, chat, tid("task-rej"), "Calendar")

			tc.end(t, a, tid("task-rej"))

			waitForOutcome(t, chat, link, tc.want)
		})
	}
}

func TestTaskEndingWhileTheLinkIsBeingPostedStillRetiresIt(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	st := submitForTest(t, a, tid("task-late"), dmMeta())
	var writes atomic.Int32
	release := make(chan struct{})
	chat.mu.Lock()
	chat.onWrite = func(ctx context.Context) error {
		if writes.Add(1) == 3 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}
	chat.mu.Unlock()

	a.OnStatusUpdate(t.Context(), tid("task-late"), signInEvent("authentication_required", map[string]any{"auth_uri": "https://accounts.example.com/authorize", "tool_display_name": "Calendar"}), nil)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		a.OnFinalResponse(t.Context(), tid("task-late"), &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !st.terminal.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !st.terminal.Load() {
		t.Fatal("the task never ended")
	}
	close(release)
	<-ended

	chat.waitFor(t, func(writes []fakeWrite, _ []string) bool {
		return slices.ContainsFunc(writes, func(w fakeWrite) bool {
			return w.op == "update" && w.replacedCards && w.cardText == msgSignInIncomplete && w.cardButtons == 0
		})
	})
}

func TestSignInLinkIsReplacedWhenTheItemsCannotBeRead(t *testing.T) {
	t.Run("the read fails", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		flaky := &flakyItems{taskItemStore: a.items}
		a.items = flaky
		submitForTest(t, a, tid("task-blind"), dmMeta())
		link := askToSignIn(t, a, chat, tid("task-blind"), "Calendar")
		flaky.mu.Lock()
		flaky.listFailures = 100
		flaky.mu.Unlock()

		a.OnStatusUpdate(t.Context(), tid("task-blind"), signInResult(true), nil)

		waitForOutcome(t, chat, link, msgSignInAuthorized)
	})

	t.Run("there is no database", func(t *testing.T) {
		chat := newFakeChat()
		a := newTestAdapter(t, store.TestDB(t), chat)
		a.items = nil
		submitForTest(t, a, tid("task-nodb"), dmMeta())
		link := askToSignIn(t, a, chat, tid("task-nodb"), "Calendar")

		a.OnStatusUpdate(t.Context(), tid("task-nodb"), signInResult(true), nil)

		waitForOutcome(t, chat, link, msgSignInAuthorized)
	})
}

func TestEachSignInLinkKeepsItsOwnOutcome(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-own"), dmMeta())
	calendar := askToSignIn(t, a, chat, tid("task-own"), "Calendar")
	setCardWrites(chat, &permanentError{err: errors.New("rejected")})
	a.OnStatusUpdate(t.Context(), tid("task-own"), signInResult(true), nil)
	waitUntilAttempted(t, chat, calendar, 1)
	setCardWrites(chat, nil)
	drive := askToSignIn(t, a, chat, tid("task-own"), "Drive")

	a.OnStatusUpdate(t.Context(), tid("task-own"), signInResult(false), nil)

	waitForOutcome(t, chat, drive, msgSignInIncomplete)
	waitForOutcome(t, chat, calendar, msgSignInAuthorized)
}

func TestSignInLinkReplacedHereIsNotRewrittenWhenItsItemRemains(t *testing.T) {
	chat := newFakeChat()
	db := store.TestDB(t)
	a := newTestAdapter(t, db, chat)
	flaky := &flakyItems{taskItemStore: a.items}
	a.items = flaky
	submitForTest(t, a, tid("task-keep"), dmMeta())
	link := askToSignIn(t, a, chat, tid("task-keep"), "Calendar")
	waitForItem(t, db, tid("task-keep"), itemSignInMsgPrefix+link, link)
	flaky.setFailures("", itemSignInMsgPrefix+link)

	a.OnStatusUpdate(t.Context(), tid("task-keep"), signInResult(true), nil)
	waitForOutcome(t, chat, link, msgSignInAuthorized)
	a.OnFinalResponse(t.Context(), tid("task-keep"), &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil)

	if got := chat.updatesAttempted(link); got != 1 {
		t.Errorf("updates attempted on the link = %d, want only the one that said it was authorized", got)
	}
}

func TestSignInLinkIsReplacedWhenHandlingTheErrorPanics(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-panic"), dmMeta())
	link := askToSignIn(t, a, chat, tid("task-panic"), "Calendar")

	a.OnError(t.Context(), tid("task-panic"), nil, nil)

	waitForOutcome(t, chat, link, msgSignInIncomplete)
}

func TestTaskItemsThatAreGoneDoNotSayNoLinkIsOutstanding(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())

	links, ok := a.readSignInMessages(t.Context(), tid("task-gone"))
	if ok {
		t.Errorf("readSignInMessages = %q, true, want it unable to answer for a task with no items", links)
	}
	known, ok := a.outstandingSignInLinks(t.Context(), tid("task-gone"), []string{"m/a"}, &queueAnchors{})
	if ok {
		t.Errorf("outstandingSignInLinks = %q, true, want it unable to answer", known)
	}
}

func TestSettledLinksItemDoesNotComeBackWhenAnotherLinkIsPosted(t *testing.T) {
	db := store.TestDB(t)
	chat := newFakeChat()
	replicaA := newTestAdapter(t, db, chat)
	replicaB := newTestAdapter(t, db, chat)
	submitForTest(t, replicaB, tid("task-two-links"), dmMeta())
	first := askToSignIn(t, replicaB, chat, tid("task-two-links"), "Calendar")
	waitForItem(t, db, tid("task-two-links"), itemSignInMsgPrefix+first, first)
	if err := replicaA.LoadTask(t.Context(), tid("task-two-links")); err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	replicaA.OnStatusUpdate(t.Context(), tid("task-two-links"), signInResult(true), nil)
	waitForItemGone(t, db, tid("task-two-links"), itemSignInMsgPrefix+first)

	second := askToSignIn(t, replicaB, chat, tid("task-two-links"), "Drive")
	waitForItem(t, db, tid("task-two-links"), itemSignInMsgPrefix+second, second)

	items, err := store.NewEntrypointTaskItemRepo(db).List(t.Context(), tid("task-two-links"))
	if err != nil {
		t.Fatal(err)
	}
	if got := signInMessagesIn(items); !slices.Equal(got, []string{second}) {
		t.Errorf("recorded links = %q, want only the new one %q", got, second)
	}
	if got := chat.updatesAttempted(first); got != 1 {
		t.Errorf("updates attempted on the settled link = %d, want 1", got)
	}
}
