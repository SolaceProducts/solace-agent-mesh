// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	logReplyLostUnwritten = "google chat reply lost: the task's last messages could not be written"
	logReplyLostStopped   = logReplyUnfinished
	logReplyRejected      = "google chat reply lost: Chat rejected part of the task's last messages"
)

func lingeringFinalReply(t *testing.T, a *Adapter, chat *fakeChat) chan struct{} {
	t.Helper()
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	release := holdWrites(chat)

	q := a.queueFor(tid("task-1"), st)
	q.Replace("The final answer")
	a.drainTerminal(tid("task-1"), st, q, nil, nil)
	a.finishTask(tid("task-1"))
	return release
}

func waitForLog(t *testing.T, logs *logCapture, level slog.Level, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for logs.count(level, msg) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no %s log %q", level, msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStop_LetsALingeringFinalReplyFinish(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := recordLogs(a)
	a.lingerTimeout = time.Minute
	release := lingeringFinalReply(t, a, chat)

	stopped := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.Stop(ctx)
		close(stopped)
	}()
	for a.ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	close(release)

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	if _, texts := chat.snapshot(); !anyTextContains(texts, "The final answer") {
		t.Errorf("messages = %q, want the final answer written before Stop returned", texts)
	}
	if n := logs.count(slog.LevelError, "google chat reply lost"); n != 0 {
		t.Errorf("reply-lost errors = %d, want none", n)
	}
}

func TestStop_AbandonsALingeringFinalReplyAtItsDeadlineAndSaysSo(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := recordLogs(a)
	a.lingerTimeout = time.Minute
	lingeringFinalReply(t, a, chat)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = a.Stop(ctx)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Stop took %v, want about its 200ms deadline", elapsed)
	}
	if n := logs.count(slog.LevelError, logReplyLostStopped); n != 1 {
		t.Errorf("reply-lost errors = %d, want 1", n)
	}
}

func TestDrainTerminal_ReplyStillUnwrittenAfterTheLingerIsLoggedAsLost(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := recordLogs(a)
	a.lingerTimeout = 100 * time.Millisecond
	lingeringFinalReply(t, a, chat)

	waitForLog(t, logs, slog.LevelError, logReplyLostUnwritten)
}

func TestDrainTerminal_AfterStopBeganReportsTheReplyLost(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := recordLogs(a)
	a.lingerTimeout = time.Minute
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	lingeringFinalReply(t, a, chat)

	waitForLog(t, logs, slog.LevelError, logReplyLostStopped)
}

func TestDrainTerminal_RejectedFinalAnswerIsLoggedAsLostOnce(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := recordLogs(a)
	a.lingerTimeout = time.Minute
	st := submitForTest(t, a, tid("task-1"), dmMeta())
	chat.mu.Lock()
	chat.rejectText = answerChunks(longAnswer())[1]
	chat.mu.Unlock()

	q := a.queueFor(tid("task-1"), st)
	q.Replace(longAnswer())
	a.drainTerminal(tid("task-1"), st, q, nil, nil)
	a.finishTask(tid("task-1"))
	if err := a.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if n := logs.count(slog.LevelError, "google chat reply lost"); n != 1 {
		t.Errorf("reply-lost errors = %d, want 1", n)
	}
	if n := logs.count(slog.LevelError, logReplyRejected); n != 1 {
		t.Errorf("rejected-reply errors = %d, want 1", n)
	}
}

func TestAwaitLinger_PrefersAFinishedDrainOverTheAbandon(t *testing.T) {
	a := newTestAdapter(t, store.TestDB(t), newFakeChat())
	logs := recordLogs(a)
	a.abandonCancel()

	for range 64 {
		drained := make(chan drainResult, 1)
		drained <- drainWritten
		a.awaitLinger(tid("task-1"), drained, 0, false)
	}

	if n := logs.count(slog.LevelError, "google chat reply lost"); n != 0 {
		t.Errorf("reply-lost errors = %d, want none for replies that were written", n)
	}
}

func TestStop_ReplyHandedOffWhileStopWaitsForWorkersStillLands(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := recordLogs(a)
	a.lingerTimeout = time.Minute
	worker := make(chan struct{})
	a.ingressWG.Go(func() { <-worker })

	stopped := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.Stop(ctx)
		close(stopped)
	}()
	for a.ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	release := lingeringFinalReply(t, a, chat)
	close(release)
	close(worker)

	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	if _, texts := chat.snapshot(); !anyTextContains(texts, "The final answer") {
		t.Errorf("messages = %q, want the final answer written before Stop returned", texts)
	}
	if n := logs.count(slog.LevelError, "google chat reply lost"); n != 0 {
		t.Errorf("reply-lost errors = %d, want none", n)
	}
}
