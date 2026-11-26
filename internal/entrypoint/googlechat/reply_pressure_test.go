// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestReplyQueue_SlowWriteCoalescesTheTextBufferedBehindIt(t *testing.T) {
	const slowWrite = textFlushInterval + 100*time.Millisecond
	chat := newFakeChat()
	chat.onWrite = func(ctx context.Context) error {
		select {
		case <-time.After(slowWrite):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	q, _ := newTestQueue(chat)
	t.Cleanup(q.Close)

	deltas := 2*replyQueueOpsCap + 1
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for range deltas {
			q.Append("x")
		}
	}()
	select {
	case <-sent:
	case <-time.After(2*slowWrite + time.Second):
		t.Fatalf("sending %d deltas was still held after two writes: each delta is written on its own", deltas)
	}
	if res := q.Drain(3 * slowWrite); res != drainWritten {
		t.Fatalf("drain = %v, want the answer written", res)
	}

	writes, texts := chat.snapshot()
	if len(writes) > 3 {
		t.Errorf("writes = %d, want the deltas coalesced into at most 3", len(writes))
	}
	if len(texts) != 1 || texts[0] != strings.Repeat("x", deltas) {
		t.Errorf("messages = %d (first %d runes), want one holding all %d deltas", len(texts), len(firstOr(texts)), deltas)
	}
}

func firstOr(texts []string) string {
	if len(texts) == 0 {
		return ""
	}
	return texts[0]
}

func TestAbsorbTextStopsAtTheFirstOpThatIsNotText(t *testing.T) {
	q := &replyQueue{ops: make(chan replyOp, 8)}
	q.ops <- replyOp{kind: opAppend, text: "b"}
	q.ops <- replyOp{kind: opReplace, text: "cd"}
	q.ops <- replyOp{kind: opNotice, text: "notice"}
	q.ops <- replyOp{kind: opAppend, text: "e"}
	q.text = "a"

	q.absorbText()

	if q.text != "cd" || !q.dirty {
		t.Errorf("text = %q (dirty %v), want the text ops before the notice applied", q.text, q.dirty)
	}
	if q.lookahead == nil || q.lookahead.kind != opNotice {
		t.Fatalf("lookahead = %+v, want the notice held in its place", q.lookahead)
	}
	if n := len(q.ops); n != 1 {
		t.Errorf("ops left buffered = %d, want only the text after the notice", n)
	}
	q.absorbText()
	if q.text != "cd" || len(q.ops) != 1 {
		t.Error("text after a held op was taken ahead of it")
	}
}

func TestTerminalHandlerReturnsWhileItsWritesAreHeld(t *testing.T) {
	for name, terminate := range map[string]func(a *Adapter, ctx context.Context, taskID string){
		"final response": func(a *Adapter, ctx context.Context, taskID string) {
			a.OnFinalResponse(ctx, taskID, completedTask("The final answer"), nil)
		},
		"error": func(a *Adapter, ctx context.Context, taskID string) {
			a.OnError(ctx, taskID, &a2a.Error{Code: -32603, Message: "agent crashed"}, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			chat := newFakeChat()
			a := newTestAdapter(t, store.TestDB(t), chat)
			submitForTest(t, a, tid("task-1"), dmMeta())
			chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
			release := holdWrites(chat)
			t.Cleanup(func() { closeOnce(release) })

			ev := receiveEvent(t)
			returned := make(chan struct{})
			go func() {
				defer close(returned)
				terminate(a, ev.ctx, tid("task-1"))
			}()
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Fatal("the terminal handler waited on Chat's writes, holding its event worker")
			}
			ev.d.Release()
			if ev.acks.Load() != 0 {
				t.Fatal("the event was acknowledged before its writes were done")
			}
			requireTaskRecorded(t, a, tid("task-1"), true)

			closeOnce(release)
			ev.waitAcked(t)
			requireTaskRecorded(t, a, tid("task-1"), false)
		})
	}
}

func TestUnheldTerminalEndsTheTaskOnlyOnceItsWritesAreDone(t *testing.T) {
	chat := newFakeChat()
	a := newTestAdapter(t, store.TestDB(t), chat)
	submitForTest(t, a, tid("task-1"), dmMeta())
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	release := holdWrites(chat)
	t.Cleanup(func() { closeOnce(release) })

	a.OnError(t.Context(), tid("task-1"), &a2a.Error{Code: -32603, Message: "timed out"}, nil)
	requireTaskRecorded(t, a, tid("task-1"), true)

	closeOnce(release)
	waitTerminalWritten(t, a)
	requireTaskRecorded(t, a, tid("task-1"), false)
}

func TestReplyQueue_DeltaJustAfterASlowWriteWaitsOutTheFlushInterval(t *testing.T) {
	const slowWrite = textFlushInterval + 100*time.Millisecond
	chat := newFakeChat()
	var calls atomic.Int32
	chat.onWrite = func(ctx context.Context) error {
		if calls.Add(1) > 1 {
			return nil
		}
		select {
		case <-time.After(slowWrite):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	q, _ := newTestQueue(chat)
	t.Cleanup(q.Close)

	q.Append("a")
	chat.waitFor(t, func(w []fakeWrite, _ []string) bool { return len(w) == 1 })
	time.Sleep(200 * time.Millisecond)
	q.Append("b")
	time.Sleep(600 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("writes started = %d, want 1: a delta shortly after a slow write started another write at once", n)
	}
}
