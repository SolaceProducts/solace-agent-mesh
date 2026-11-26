// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	httpsAuthURI   = "https://idp.example/authorize"
	logPromptLost  = "google chat prompt could not be posted"
	logPromptAbort = "google chat prompt was not posted: its reply queue closed first"
	logCancelLost  = "google chat task could not be cancelled"
)

func askRequest() *a2a.UserInputRequestData {
	return &a2a.UserInputRequestData{RequestID: "req-9", Surface: json.RawMessage(askSurfaceJSON)}
}

func answeredTask(t *testing.T, meta taskMeta) (*Adapter, *fakeChat, *taskState, *replyQueue, *broker.DevBroker, broker.Queue) {
	t.Helper()
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)
	st := submitForTest(t, a, tid("task-1"), meta)
	q := a.queueFor(tid("task-1"), st)
	q.Append(longAnswer())
	if res := q.Drain(5 * time.Second); res != drainWritten {
		t.Fatalf("the answer did not drain: %v", res)
	}
	return a, chat, st, q, brk, requests
}

func rejectNextWrite(chat *fakeChat) {
	chat.mu.Lock()
	defer chat.mu.Unlock()
	chat.failures = []error{&permanentError{err: errors.New("400 invalid card")}}
}

func requireAnswerThenHalt(t *testing.T, chat *fakeChat, halt string) {
	t.Helper()
	want := append(answerChunks(longAnswer()), halt)
	if _, texts := chat.snapshot(); !slices.Equal(texts, want) {
		t.Fatalf("got %d messages (first starts with the answer: %v), want the %d answer chunks intact, then %q",
			len(texts), len(texts) > 0 && texts[0] == want[0], len(want)-1, halt)
	}
}

func TestPromptForInput_CardRejectedAfterAStreamedAnswerKeepsTheAnswer(t *testing.T) {
	a, chat, st, q, brk, requests := answeredTask(t, dmMeta())
	rejectNextWrite(chat)

	a.promptForInput(t.Context(), tid("task-1"), st, q, askRequest())
	q.Drain(5 * time.Second)

	requireAnswerThenHalt(t, chat, msgQuestionUnavailable)
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForInput_UnreadableSurfaceAfterAStreamedAnswerKeepsTheAnswer(t *testing.T) {
	a, chat, st, q, brk, requests := answeredTask(t, dmMeta())

	a.promptForInput(t.Context(), tid("task-1"), st, q, &a2a.UserInputRequestData{RequestID: "req-1", Surface: json.RawMessage(`{not json`)})
	q.Drain(5 * time.Second)

	requireAnswerThenHalt(t, chat, msgQuestionUnavailable)
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForInput_UnanswerableQuestionAfterAStreamedAnswerKeepsTheAnswer(t *testing.T) {
	meta := dmMeta()
	meta.AddOn = true
	a, chat, st, q, brk, requests := answeredTask(t, meta)

	a.promptForInput(t.Context(), tid("task-1"), st, q, askRequest())
	q.Drain(5 * time.Second)

	requireAnswerThenHalt(t, chat, msgQuestionNeedsButtons)
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForAuthorization_CardRejectedAfterAStreamedAnswerKeepsTheAnswer(t *testing.T) {
	a, chat, st, q, brk, requests := answeredTask(t, dmMeta())
	rejectNextWrite(chat)

	a.promptForAuthorization(t.Context(), tid("task-1"), st, q, &a2a.AuthenticationRequiredData{AuthURI: httpsAuthURI})
	q.Drain(5 * time.Second)

	requireAnswerThenHalt(t, chat, msgSignInUnavailable)
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForAuthorization_UnpresentableLinkAfterAStreamedAnswerKeepsTheAnswer(t *testing.T) {
	a, chat, st, q, brk, requests := answeredTask(t, dmMeta())

	a.promptForAuthorization(t.Context(), tid("task-1"), st, q, &a2a.AuthenticationRequiredData{AuthURI: "http://idp.example/authorize"})
	q.Drain(5 * time.Second)

	requireAnswerThenHalt(t, chat, msgSignInUnavailable)
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForAuthorization_CardChatRejectsStopsTheTask(t *testing.T) {
	chat := newFakeChat()
	chat.failures = []error{nil, nil, &permanentError{err: errors.New("400 invalid card")}}
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)

	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	a.promptForAuthorization(t.Context(), tid("task-1"), st, q, &a2a.AuthenticationRequiredData{AuthURI: httpsAuthURI})
	q.Drain(5 * time.Second)

	if _, texts := chat.snapshot(); !slices.Equal(texts, []string{msgSignInUnavailable}) {
		t.Errorf("messages = %q, want only the status message, telling the user the request can't continue", texts)
	}
	if st.snapshot().Prompted {
		t.Error("the task is recorded as prompted although its sign-in link was never shown")
	}
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptWriteCutShortByItsQueueClosing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		close     func(a *Adapter)
		wantAcked bool
	}{
		{name: "adapter stops", close: func(a *Adapter) {
			if err := a.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "task forgotten", close: func(a *Adapter) { a.forgetTask(tid("task-1")) }, wantAcked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			brk, requests := attachCore(t, a)
			logs := recordLogs(a)
			st := submitForTest(t, a, tid("task-1"), dmMeta())
			q := a.queueFor(tid("task-1"), st)
			q.Drain(5 * time.Second)

			entered := make(chan struct{}, 1)
			chat.mu.Lock()
			chat.onWrite = func(ctx context.Context) error {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return ctx.Err()
			}
			chat.mu.Unlock()
			var acks atomic.Int32
			ctx, d := delivery.Attach(t.Context(), "m1", false, func() { acks.Add(1) })
			a.promptForInput(ctx, tid("task-1"), st, q, askRequest())
			d.Release()
			<-entered
			tc.close(a)

			if n := logs.count(slog.LevelError, logPromptLost); n != 0 {
				t.Errorf("prompt-refused errors = %d, want none for a write its queue closing cut short", n)
			}
			if got := acks.Load() == 1; got != tc.wantAcked {
				t.Errorf("event acknowledged = %v, want %v", got, tc.wantAcked)
			}
			if !tc.wantAcked {
				requireNoAgentRequest(t, brk, requests)
				return
			}
			if n := logs.count(slog.LevelWarn, logPromptAbort); n != 1 {
				t.Errorf("queue-closed warnings = %d, want 1", n)
			}
			requireCancelPublished(t, brk, requests, tid("task-1"))
		})
	}
}

func TestPromptOrStop_FallbackWithoutStopDoesNotPanic(t *testing.T) {
	chat := newFakeChat()
	chat.failures = []error{nil, nil, &permanentError{err: errors.New("400")}}
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.PromptOrStop(msgQuestionPrefix+"Pick one", "Pick one", authorizeCard("Sign in", "https://idp.example/authorize"), promptFallback{halt: "stopped"}, nil)
	if res := q.Drain(5 * time.Second); res == drainUnfinished {
		t.Fatal("the queue did not finish: its goroutine died on the refused card")
	}
}

func TestCancelTask_StillPublishesOnceTheAdapterIsStopping(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk := broker.NewDevBroker()
	requests := attachCoreOn(t, a, brk, ctxHonoringBroker{brk})
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	a.cancel()

	a.promptForInput(t.Context(), tid("task-1"), st, q, &a2a.UserInputRequestData{RequestID: "req-1", Surface: json.RawMessage(`{not json`)})

	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestCancelTask_FailureIsLoggedAsAnError(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, _ := attachCore(t, a)
	logs := recordLogs(a)
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	if err := brk.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}

	a.promptForInput(t.Context(), tid("task-1"), st, q, &a2a.UserInputRequestData{RequestID: "req-1", Surface: json.RawMessage(`{not json`)})

	if n := logs.count(slog.LevelError, logCancelLost); n != 1 {
		t.Errorf("cancel-failure errors = %d, want 1: the task now waits out its timeout", n)
	}
}
