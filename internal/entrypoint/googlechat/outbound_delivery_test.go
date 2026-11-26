// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func longAnswer() string { return strings.Repeat("sentence of words. ", 700) }

func answerChunks(text string) []string { return splitForChat(renderForChat(text), maxMessageRunes) }

func TestReplyQueue_FailedChunkIsRetriedInOrderWithoutAGap(t *testing.T) {
	chat := newFakeChat()
	chat.failures = []error{nil, nil, &permanentError{err: errors.New("500")}}
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(longAnswer())
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	want := answerChunks(longAnswer())
	_, texts := chat.snapshot()
	if len(texts) != len(want) {
		t.Fatalf("messages = %d, want %d: one per chunk, with the failed one retried", len(texts), len(want))
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("message %d holds the wrong chunk", i)
		}
	}
}

func TestReplyQueue_ChunkStillFailingAtDrainTellsTheUserOnce(t *testing.T) {
	chat := newFakeChat()
	chunkFailure := &permanentError{err: errors.New("500")}
	chat.failures = []error{nil, nil, chunkFailure, chunkFailure, nil, chunkFailure}
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(longAnswer())
	for range 3 {
		q.Drain(5 * time.Second)
	}

	_, texts := chat.snapshot()
	notices := 0
	for _, text := range texts {
		if text == msgReplyIncomplete {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("incomplete-reply notices = %d, want 1: %q", notices, texts)
	}
	for i, chunk := range answerChunks(longAnswer()) {
		if !anyTextContains(texts, chunk) {
			t.Errorf("chunk %d never reached Chat after the retry", i)
		}
	}
}

func TestReplyQueue_NoticeAfterAFailedChunkKeepsTheNoticeAndTheWholeAnswer(t *testing.T) {
	chat := newFakeChat()
	chunkFailure := &permanentError{err: errors.New("500")}
	chat.failures = []error{nil, nil, chunkFailure, chunkFailure}
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(longAnswer())
	q.Notice(msgTaskCanceled)
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	want := append(answerChunks(longAnswer()), msgTaskCanceled)
	if _, texts := chat.snapshot(); !slices.Equal(texts, want) {
		t.Fatalf("got %d messages (notice shown: %v), want the %d answer chunks in order, then the notice",
			len(texts), slices.Contains(texts, msgTaskCanceled), len(want)-1)
	}
}

func TestReplyQueue_PromptAfterAFailedChunkTellsTheUser(t *testing.T) {
	chat := newFakeChat()
	chat.rejectText = answerChunks(longAnswer())[1]
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(longAnswer())
	q.Prompt("❓ Continue?", "Continue?", nil)
	q.Drain(5 * time.Second)

	_, texts := chat.snapshot()
	notices := 0
	for _, text := range texts {
		if text == msgReplyIncomplete {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("incomplete-reply notices = %d across %d messages, want 1", notices, len(texts))
	}
}

func TestReplyQueue_DrainHonorsItsTimeoutWhenTheBufferIsFull(t *testing.T) {
	chat := newFakeChat()
	release := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	chat.onWrite = func(context.Context) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}
	q, _ := newTestQueue(chat)
	defer func() {
		close(release)
		q.Close()
	}()

	q.Placeholder("⏳ Working")
	<-entered
	for range replyQueueOpsCap {
		q.Progress("⏳ still working")
	}

	drained := make(chan drainResult, 1)
	go func() { drained <- q.Drain(100 * time.Millisecond) }()
	select {
	case res := <-drained:
		if res != drainUnfinished {
			t.Fatal("Drain reported success while a write was held")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Drain with a full buffer did not return within 2s, want about its 100ms timeout")
	}
}

func TestDrainTerminal_SlowFinalWriteFinishesAfterTheTaskIsForgotten(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	a.lingerTimeout = 5 * time.Second

	st := submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	release := holdWrites(chat)

	q := a.queueFor(tid("task-1"), st)
	q.Replace("The final answer")
	a.drainTerminal(tid("task-1"), st, q, nil, nil)
	a.finishTask(tid("task-1"))
	close(release)

	chat.waitFor(t, func(_ []fakeWrite, texts []string) bool { return anyTextContains(texts, "The final answer") })
}

func holdWrites(chat *fakeChat) chan struct{} {
	release := make(chan struct{})
	chat.mu.Lock()
	defer chat.mu.Unlock()
	chat.onWrite = func(ctx context.Context) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return release
}

func TestPromptForAuthorization_UnpresentableLinkStopsTheTask(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)

	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	a.promptForAuthorization(t.Context(), tid("task-1"), st, q, &a2a.AuthenticationRequiredData{AuthURI: "http://idp.example/authorize"})
	q.Drain(5 * time.Second)

	if _, texts := chat.snapshot(); !anyTextContains(texts, msgSignInUnavailable) {
		t.Errorf("messages = %q, want the user told the request can't continue", texts)
	}
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForInput_UnreadableSurfaceStopsTheTask(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)

	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	a.promptForInput(t.Context(), tid("task-1"), st, q, &a2a.UserInputRequestData{RequestID: "req-1", Surface: json.RawMessage(`{not json`)})
	q.Drain(5 * time.Second)

	if _, texts := chat.snapshot(); !anyTextContains(texts, msgQuestionUnavailable) {
		t.Errorf("messages = %q, want the user told the request can't continue", texts)
	}
	requireCancelPublished(t, brk, requests, tid("task-1"))
}

func TestPromptForInput_CardChatRejectsStopsTheTask(t *testing.T) {
	chat := newFakeChat()
	chat.failures = []error{nil, nil, &permanentError{err: errors.New("400 invalid card")}}
	a := newTestAdapter(t, store.TestDB(t), chat)
	brk, requests := attachCore(t, a)

	st := submitForTest(t, a, tid("task-1"), dmMeta())
	q := a.queueFor(tid("task-1"), st)
	a.promptForInput(t.Context(), tid("task-1"), st, q, &a2a.UserInputRequestData{RequestID: "req-9", Surface: json.RawMessage(askSurfaceJSON)})
	q.Drain(5 * time.Second)

	_, texts := chat.snapshot()
	if len(texts) != 1 || texts[0] != msgQuestionUnavailable {
		t.Errorf("messages = %q, want only the status message, telling the user the request can't continue", texts)
	}
	if st.snapshot().Prompted {
		t.Error("the task is recorded as prompted although its question was never shown")
	}
	requireCancelPublished(t, brk, requests, tid("task-1"))

	q.Notice(msgTaskCanceled)
	q.Drain(5 * time.Second)
	if _, texts := chat.snapshot(); !slices.Equal(texts, []string{msgQuestionUnavailable, msgTaskCanceled}) {
		t.Errorf("messages after the cancel = %q, want the explanation kept and the cancel notice below it", texts)
	}
}

func TestOnError_ClearsTheCancelControl(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-1"), dmMeta())
	writes, _ := chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	status := writes[0].name
	if writes[0].cards != 1 {
		t.Fatalf("placeholder = %+v, want it posted with the cancel control", writes[0])
	}

	a.OnError(t.Context(), tid("task-1"), &a2a.Error{Message: "agent crashed"}, nil)

	chat.waitFor(t, func(w []fakeWrite, _ []string) bool {
		return slices.ContainsFunc(w, func(x fakeWrite) bool { return x.name == status && x.replacedCards && x.cards == 0 })
	})
}

func countText(texts []string, want string) int {
	n := 0
	for _, text := range texts {
		if text == want {
			n++
		}
	}
	return n
}

func TestReplyQueue_NoticeBehindAChunkThatNeverLandsIsPostedAtTheDrain(t *testing.T) {
	chat := newFakeChat()
	chat.rejectText = answerChunks(longAnswer())[1]
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(longAnswer())
	q.Notice(msgTaskCanceled)
	res := q.Drain(5 * time.Second)

	_, texts := chat.snapshot()
	if n := countText(texts, msgReplyIncomplete); n != 1 {
		t.Errorf("incomplete-reply notices = %d, want 1", n)
	}
	if !slices.Contains(texts, msgTaskCanceled) {
		t.Errorf("messages = %q, want the cancel notice shown although a chunk never landed", texts)
	}
	if res != drainIncomplete {
		t.Errorf("Drain = %v, want drainIncomplete for a chunk Chat never took", res)
	}
}

func TestReplyQueue_DrainReportsANoticeChatRejectedInTheStatusMessage(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()
	q.Placeholder("⏳ Working")
	q.Drain(5 * time.Second)

	chat.mu.Lock()
	chat.rejectText = msgAgentError
	chat.mu.Unlock()
	q.Notice(msgAgentError)

	if res := q.Drain(5 * time.Second); res != drainIncomplete {
		t.Errorf("Drain = %v, want drainIncomplete for a terminal notice Chat rejected", res)
	}
}

func TestReplyQueue_DrainReportsAStandaloneNoticeChatRejected(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()
	q.Placeholder("⏳ Working")
	q.Append("Half an answer")
	q.Drain(5 * time.Second)

	chat.mu.Lock()
	chat.rejectText = msgTaskCanceled
	chat.mu.Unlock()
	q.Notice(msgTaskCanceled)

	if res := q.Drain(5 * time.Second); res != drainIncomplete {
		t.Errorf("Drain = %v, want drainIncomplete for a terminal notice Chat rejected", res)
	}
}

func TestReplyQueue_NoticeWaitingOnAChunkIsPostedAboveTheNextPrompt(t *testing.T) {
	const notice, after = "NOTICE-A", "ANSWER-AFTER-PROMPT"
	chat := newFakeChat()
	chat.rejectText = answerChunks(longAnswer())[1]
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(longAnswer())
	q.Notice(notice)
	q.Prompt("❓ Q", "Q", nil)
	q.Append(after)
	q.Drain(5 * time.Second)

	writes, _ := chat.snapshot()
	at := func(match func(fakeWrite) bool) int {
		return slices.IndexFunc(writes, func(w fakeWrite) bool { return w.op == "create" && match(w) })
	}
	noticeAt := at(func(w fakeWrite) bool { return w.text == notice })
	promptAt := at(func(w fakeWrite) bool { return w.fallback == "Q" })
	afterAt := at(func(w fakeWrite) bool { return w.text == after })
	if noticeAt < 0 || promptAt < 0 || afterAt < 0 || noticeAt >= promptAt || promptAt >= afterAt {
		t.Errorf("posted notice at %d, prompt at %d, later answer at %d; want them in that order", noticeAt, promptAt, afterAt)
	}
}
