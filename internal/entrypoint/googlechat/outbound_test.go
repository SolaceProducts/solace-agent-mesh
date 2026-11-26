// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	chat "google.golang.org/api/chat/v1"
)

func newTestQueue(chat *fakeChat) (*replyQueue, *[]string) {
	return newTestQueueFrom(chat, queueAnchors{})
}

func newTestQueueFrom(chat *fakeChat, start queueAnchors) (*replyQueue, *[]string) {
	writer := newChatWriter(chat, newSpaceThrottle(0), slog.New(slog.DiscardHandler))
	writer.retryDelay = func(int, time.Duration) time.Duration { return time.Millisecond }
	var first []string
	q := newReplyQueue("spaces/S", "spaces/S/threads/T", writer, slog.New(slog.DiscardHandler), start, func(a queueAnchors) {
		if a.Status != "" {
			first = append(first, a.Status)
		}
	})
	return q, &first
}

func TestReplyQueue_LongAnswerRollsOverIntoNewMessages(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Replace(strings.Repeat("sentence of words. ", 700))
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	_, texts := chat.snapshot()
	if len(texts) < 3 {
		t.Fatalf("messages = %d, want the answer split across several", len(texts))
	}
	for i, text := range texts {
		if n := utf8.RuneCountInString(text); n > maxMessageRunes {
			t.Errorf("message %d is %d runes, over the limit", i, n)
		}
		if strings.Contains(text, "Working") {
			t.Errorf("message %d still shows the placeholder", i)
		}
	}
}

func TestReplyQueue_DeletedStatusMessageIsRepostedAndAdopted(t *testing.T) {
	chat := newFakeChat()
	q, first := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Drain(5 * time.Second)
	writes, _ := chat.snapshot()
	chat.deleteMessage(writes[0].name)

	q.Replace("The answer.")
	q.Drain(5 * time.Second)

	_, texts := chat.snapshot()
	if len(texts) != 1 || texts[0] != "The answer." {
		t.Fatalf("messages = %q, want the answer reposted once", texts)
	}
	if len(*first) != 2 {
		t.Errorf("status message callbacks = %v, want the repost to be adopted as the new status message", *first)
	}
}

func TestReplyQueue_RetriesRateLimitedWrite(t *testing.T) {
	chat := newFakeChat()
	chat.failures = []error{&rateLimitedError{err: errors.New("429")}, &rateLimitedError{err: errors.New("429")}}
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Drain(5 * time.Second)

	if _, texts := chat.snapshot(); len(texts) != 1 {
		t.Fatalf("messages = %q, want the write to succeed after the 429s", texts)
	}
}

func TestReplyQueue_PermanentFailureIsNotRetried(t *testing.T) {
	chat := newFakeChat()
	chat.failures = []error{&permanentError{err: errors.New("403")}}
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Drain(5 * time.Second)

	if writes, _ := chat.snapshot(); len(writes) != 0 {
		t.Fatalf("writes = %+v, want none after a permanent failure", writes)
	}
	if len(chat.failures) != 0 {
		t.Error("the failure was not consumed")
	}
}

func TestReplyQueue_NoticeAfterAnswerIsPostedSeparately(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Append("Half an answer")
	q.Notice(msgTaskCanceled)
	q.Drain(5 * time.Second)

	_, texts := chat.snapshot()
	if len(texts) != 2 || texts[0] != "Half an answer" || texts[1] != msgTaskCanceled {
		t.Fatalf("messages = %q, want the partial answer kept and the notice after it", texts)
	}
}

func TestReplyQueue_AnswerAfterPromptLandsBelowIt(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Prompt("❓ Which day?", "Which day?", nil)
	q.Progress("⏳ Booking")
	q.Replace("Booked for Thursday.")
	q.Drain(5 * time.Second)

	writes, texts := chat.snapshot()
	if len(texts) != 3 || texts[0] != "❓ Which day?" || texts[1] != "" || texts[2] != "Booked for Thursday." {
		t.Fatalf("messages = %q, want the status relabelled as the prompt's lead, then the prompt card, then the answer", texts)
	}
	if !slices.ContainsFunc(writes, func(w fakeWrite) bool { return w.op == "create" && w.fallback == "Which day?" }) {
		t.Errorf("writes = %+v, want the prompt card posted", writes)
	}
}

func TestReplyQueue_PromptKeepsAnAnswerAlreadyWritten(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Placeholder("⏳ Working")
	q.Append("Here is what I found so far.")
	q.Prompt("❓ Continue?", "Continue?", nil)
	q.Replace("All done.")
	q.Drain(5 * time.Second)

	_, texts := chat.snapshot()
	if len(texts) != 3 || texts[0] != "Here is what I found so far." || texts[1] != "" || texts[2] != "All done." {
		t.Fatalf("messages = %q, want the earlier answer kept above the prompt and the new one below", texts)
	}
}

func TestSpaceThrottle_SpacesWritesPerSpace(t *testing.T) {
	throttle := newSpaceThrottle(40 * time.Millisecond)
	ctx := t.Context()
	start := time.Now()
	for range 3 {
		if err := throttle.wait(ctx, "spaces/A"); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("three writes to one space took %v, want at least two intervals", elapsed)
	}

	otherStart := time.Now()
	if err := throttle.wait(ctx, "spaces/B"); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(otherStart); waited > 20*time.Millisecond {
		t.Errorf("a different space waited %v, want no wait", waited)
	}
}

func TestBackoffDelay_HonorsRetryAfterAndCaps(t *testing.T) {
	if got := backoffDelay(1, 3*time.Second); got != 3*time.Second {
		t.Errorf("with Retry-After 3s = %v, want 3s", got)
	}
	if got := backoffDelay(1, time.Hour); got != outboundRetryCap {
		t.Errorf("with a huge Retry-After = %v, want the cap", got)
	}
	if got := backoffDelay(20, 0); got > outboundRetryCap+outboundRetryJitter {
		t.Errorf("late attempt = %v, want at most cap plus jitter", got)
	}
}

func newTestQueueWithControls(chat *fakeChat) *replyQueue {
	q, _ := newTestQueue(chat)
	q.controls = cancelControls("task", "agent")
	return q
}

func concludeCards() []*chat.CardWithId { return feedbackCard(tid("task-c"), "sess-1") }

func TestReplyQueue_ConcludeAfterAStreamedAnswerPutsTheCardsOnIt(t *testing.T) {
	chat := newFakeChat()
	q := newTestQueueWithControls(chat)
	defer q.Close()

	q.Placeholder("Working")
	q.Append("Hel")
	q.Append("lo.")
	q.Conclude(feedbackPrompt, concludeCards())
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	if _, texts := chat.snapshot(); !slices.Equal(texts, []string{"Hello."}) {
		t.Errorf("messages = %q, want the one answer", texts)
	}
	assertFinalCards(t, chat, []string{feedbackCardID})
}

func TestReplyQueue_ConcludePostsTheCardsBelowAnAnswerThatWasNotFullyWritten(t *testing.T) {
	chat := newFakeChat()
	chat.rejectText = "The answer."
	q := newTestQueueWithControls(chat)
	defer q.Close()

	q.Placeholder("Working")
	q.Replace("The answer.")
	q.Conclude(feedbackPrompt, concludeCards())
	q.Drain(5 * time.Second)

	cards := chat.cardsByMessage()
	if len(cards) == 0 || len(cards[0]) != 0 {
		t.Fatalf("cards on the first message = %q, want none", cards)
	}
	if n := countMessagesWith(cards, feedbackCardID); n != 1 {
		t.Errorf("messages carrying the feedback card = %d (%q), want one, below the first", n, cards)
	}
}

func countMessagesWith(cards [][]string, id string) int {
	n := 0
	for _, ids := range cards {
		if slices.Contains(ids, id) {
			n++
		}
	}
	return n
}

func TestReplyQueue_ConcludeAfterTakingOverClearsTheOldControls(t *testing.T) {
	chat := newFakeChat()
	old, err := chat.CreateMessage(t.Context(), "spaces/S", "", messageContent{Text: "old status", Cards: cancelControls("task", "agent")})
	if err != nil {
		t.Fatal(err)
	}
	q, _ := newTestQueueFrom(chat, queueAnchors{Controls: old, Prompted: true})
	defer q.Close()

	q.Replace("The answer.")
	q.Conclude(feedbackPrompt, concludeCards())
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	assertFinalCards(t, chat, nil, []string{feedbackCardID})
}

func TestReplyQueue_ConcludeWithNothingWrittenPostsTheCards(t *testing.T) {
	chat := newFakeChat()
	q, _ := newTestQueue(chat)
	defer q.Close()

	q.Conclude(feedbackPrompt, concludeCards())
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	assertFinalCards(t, chat, []string{feedbackCardID})
}

func TestReplyQueue_ConcludeAfterAPromptWithNoLaterTextPostsTheCardsBelowIt(t *testing.T) {
	chat := newFakeChat()
	q := newTestQueueWithControls(chat)
	defer q.Close()

	q.Placeholder("Working")
	q.Replace("Before the prompt.")
	q.Prompt("lead", "needs input", authorizeCard("Sign in", "https://example.com/consent"))
	q.Conclude(feedbackPrompt, concludeCards())
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	assertFinalCards(t, chat, nil, []string{"sam-authorize"}, []string{feedbackCardID})
}

func TestReplyQueue_ClearControlsAfterConcludeKeepsTheCards(t *testing.T) {
	chat := newFakeChat()
	q := newTestQueueWithControls(chat)
	defer q.Close()

	q.Placeholder("Working")
	q.Replace("Done.")
	q.Conclude(feedbackPrompt, concludeCards())
	q.ClearControls()
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	assertFinalCards(t, chat, []string{feedbackCardID})
}

func TestReplyQueue_ConcludeAfterOnlyANoticePutsTheCardsOnIt(t *testing.T) {
	chat := newFakeChat()
	q := newTestQueueWithControls(chat)
	defer q.Close()

	q.Placeholder("Working")
	q.Notice(msgNoDisplayableReply)
	q.Conclude(feedbackPrompt, concludeCards())
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}

	if _, texts := chat.snapshot(); !slices.Equal(texts, []string{msgNoDisplayableReply}) {
		t.Errorf("messages = %q, want the notice alone", texts)
	}
	assertFinalCards(t, chat, []string{feedbackCardID})
}

func TestReplyQueue_ConcludeAfterTheAnswerWasDeletedPostsTheCards(t *testing.T) {
	chat := newFakeChat()
	q := newTestQueueWithControls(chat)
	defer q.Close()

	q.Placeholder("Working")
	q.Replace("Done.")
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}
	writes, _ := chat.snapshot()
	chat.deleteMessage(writes[0].name)

	q.Conclude(feedbackPrompt, concludeCards())
	q.Drain(5 * time.Second)

	assertFinalCards(t, chat, []string{feedbackCardID})
}

func TestReplyQueue_ConcludeStoppedMidWriteLogsItAtDebug(t *testing.T) {
	chat := newFakeChat()
	var blockCards atomic.Bool
	chat.onWrite = func(ctx context.Context) error {
		if blockCards.Load() {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	var logs syncBuffer
	writer := newChatWriter(chat, newSpaceThrottle(0), slog.New(slog.DiscardHandler))
	q := newReplyQueue("spaces/S", "spaces/S/threads/T", writer, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), queueAnchors{}, nil)
	q.controls = cancelControls("task", "agent")

	q.Placeholder("Working")
	q.Replace("Done.")
	if q.Drain(5*time.Second) != drainWritten {
		t.Fatal("queue did not drain")
	}
	blockCards.Store(true)
	q.Conclude(feedbackPrompt, concludeCards())
	time.Sleep(50 * time.Millisecond)
	q.Close()

	if got := logs.String(); !strings.Contains(got, "the reply queue stopped") || strings.Contains(got, "level=WARN") {
		t.Errorf("logs = %q, want the stop reported at debug and no warning", got)
	}
}

func TestPromptLeftInAStoppedQueueIsSettled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stop      func(q *replyQueue)
		wantFreed bool
	}{
		{name: "closed", stop: (*replyQueue).Close, wantFreed: true},
		{name: "abandoned", stop: (*replyQueue).Abandon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chat := newFakeChat()
			holdWrites(chat)
			q, _ := newTestQueue(chat)
			q.Placeholder("⏳ Working")
			chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return true })
			var buffered, late atomic.Int32
			q.PromptOrStop("❓ Q", "Q", nil, promptFallback{halt: "stopped", stop: func(error) {}}, func() { buffered.Add(1) })
			tc.stop(q)
			q.PromptOrStop("❓ Q", "Q", nil, promptFallback{halt: "stopped", stop: func(error) {}}, func() { late.Add(1) })

			want := int32(0)
			if tc.wantFreed {
				want = 1
			}
			if buffered.Load() != want || late.Load() != want {
				t.Errorf("releases: buffered = %d, sent after the stop = %d, want %d each", buffered.Load(), late.Load(), want)
			}
		})
	}
}
