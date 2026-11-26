// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	logQueuePanicked  = "google chat reply queue panicked; the task's remaining writes are given up"
	logWriterPanicked = "google chat task end panicked; whatever was not yet written is given up"
)

func TestCreateMessage_SuccessWithoutAMessageIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "null")
	}))
	t.Cleanup(srv.Close)
	api, err := newRestChatAPI(t.Context(), &Config{ChatAPIBaseURL: srv.URL + "/", TestSkipGoogleAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	name, err := api.CreateMessage(t.Context(), "spaces/S", "", messageContent{Text: "hello"})
	if !errors.Is(err, errCreatedMessageMissing) || name != "" {
		t.Fatalf("CreateMessage = %q, %v, want %v", name, err, errCreatedMessageMissing)
	}
	if !isTerminalWriteError(err) {
		t.Error("a create that may have posted its message is retried, which could post it twice")
	}
}

func TestReplyQueuePanicGivesUpItsWritesAndReleasesTheirEvents(t *testing.T) {
	chat := newFakeChat()
	gate := make(chan struct{})
	chat.onWrite = func(context.Context) error {
		<-gate
		panic("boom in write")
	}
	var logs bytes.Buffer
	writer := newChatWriter(chat, newSpaceThrottle(0), slog.New(slog.DiscardHandler))
	q := newReplyQueue("spaces/S", "spaces/S/threads/T", writer, slog.New(slog.NewJSONHandler(&logs, nil)), queueAnchors{}, nil)
	t.Cleanup(q.Close)

	var released atomic.Int32
	release := func() { released.Add(1) }
	fallback := promptFallback{halt: "halt", stop: func(error) {}}
	q.PromptOrStop("lead", "first", cancelControls("t", "t"), fallback, release)
	q.PromptOrStop("lead", "second", cancelControls("t", "t"), fallback, release)
	close(gate)

	if res := q.Drain(5 * time.Second); res != drainUnfinished {
		t.Errorf("drain = %v, want the writes given up", res)
	}
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for range 2 * replyQueueOpsCap {
			q.Append("x")
		}
		q.PromptOrStop("lead", "third", cancelControls("t", "t"), fallback, release)
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("a send to the queue that panicked was held")
	}
	if n := released.Load(); n != 3 {
		t.Errorf("holds released = %d, want all 3", n)
	}

	lines := logLines(t, &logs)
	line := lineWith(t, lines, logQueuePanicked)
	if line["level"] != "ERROR" || line["panicType"] != "string" || line["stack"] == "" {
		t.Errorf("line = %v, want an ERROR with the panic's type and a stack", line)
	}
	if strings.Contains(logs.String(), "boom in write") {
		t.Error("the panic's value reached the logs")
	}
}

func TestFinalEventAcknowledgedWhenItsWritePanics(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := jsonLogsOf(a)
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	chat.mu.Lock()
	chat.onWrite = func(context.Context) error { panic("boom in write") }
	chat.mu.Unlock()

	ev := receiveEvent(t)
	a.OnFinalResponse(ev.ctx, tid("task-1"), completedTask("The final answer"), nil)
	ev.d.Release()
	ev.waitAcked(t)
	waitTerminalWritten(t, a)
	requireTaskRecorded(t, a, tid("task-1"), false)
	if line := lineWith(t, logLines(t, logs), logQueuePanicked); line["taskID"] != tid("task-1") {
		t.Errorf("line = %v, want it to name the task", line)
	}
}

func TestFinalEventAcknowledgedWhenEndingTheTaskPanics(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	logs := jsonLogsOf(a)
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	a.taskRegistry = &removeHook{TaskRegistry: a.taskRegistry, before: func(string) { panic("boom in remove") }}

	ev := receiveEvent(t)
	a.OnFinalResponse(ev.ctx, tid("task-1"), completedTask("The final answer"), nil)
	ev.d.Release()
	ev.waitAcked(t)
	waitTerminalWritten(t, a)

	lines := logLines(t, logs)
	line := lineWith(t, lines, logWriterPanicked)
	if line["level"] != "ERROR" || line["taskID"] != tid("task-1") || line["panicType"] != "string" || line["stack"] == "" {
		t.Errorf("line = %v, want an ERROR naming the task with the panic's type and a stack", line)
	}
	if strings.Contains(logs.String(), "boom in remove") {
		t.Error("the panic's value reached the logs")
	}
}

func TestReplyQueue_PanicInTextFlushReleasesTheAbsorbedPrompt(t *testing.T) {
	chat := newFakeChat()
	gate := make(chan struct{})
	var calls atomic.Int32
	chat.onWrite = func(context.Context) error {
		if calls.Add(1) == 1 {
			<-gate
			return nil
		}
		panic("boom in write")
	}
	q, _ := newTestQueue(chat)
	t.Cleanup(q.Close)

	var released atomic.Int32
	fallback := promptFallback{halt: "halt", stop: func(error) {}}
	q.Append("a")
	waitWritesStarted(t, &calls, 1)
	q.Replace("b")
	q.PromptOrStop("lead", "ask", cancelControls("t", "t"), fallback, func() { released.Add(1) })
	close(gate)
	waitQueueDone(t, q)
	if n := released.Load(); n != 1 {
		t.Fatalf("holds released = %d, want the prompt held behind the flush released once", n)
	}
}

func TestReplyQueue_PromptDroppedByAClosedQueueIsLogged(t *testing.T) {
	var logs bytes.Buffer
	writer := newChatWriter(newFakeChat(), newSpaceThrottle(0), slog.New(slog.DiscardHandler))
	q := newReplyQueue("spaces/S", "spaces/S/threads/T", writer, slog.New(slog.NewJSONHandler(&logs, nil)), queueAnchors{}, nil)
	q.Close()

	var released, stopped atomic.Int32
	q.PromptOrStop("lead", "ask", cancelControls("t", "t"), promptFallback{halt: "halt", stop: func(error) { stopped.Add(1) }}, func() { released.Add(1) })
	if released.Load() != 1 || stopped.Load() != 0 {
		t.Errorf("released %d, stopped %d; want the hold released once and stop not run", released.Load(), stopped.Load())
	}
	if line := lineWith(t, logLines(t, &logs), "google chat prompt dropped: the reply queue closed before posting it"); line["level"] != "WARN" {
		t.Errorf("line = %v, want a WARN", line)
	}
}

func waitWritesStarted(t *testing.T, calls *atomic.Int32, n int32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("write %d never started", n)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitQueueDone(t *testing.T, q *replyQueue) {
	t.Helper()
	select {
	case <-q.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the queue goroutine did not end")
	}
}
