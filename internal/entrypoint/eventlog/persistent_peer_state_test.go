// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func peerOf(a *PersistentEventLog) *PersistentEventLog {
	b := NewPersistentEventLog(nil, nil, slog.Default())
	b.repo = a.repo
	b.pollInterval = a.pollInterval
	return b
}

func peerStateLen(p *PersistentEventLog) (remoteGen, storedDone int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.remoteGen), len(p.storedDone)
}

func recvEvent(t *testing.T, ch <-chan Event, want int64) {
	t.Helper()
	select {
	case e, open := <-ch:
		if !open || e.Sequence != want {
			t.Fatalf("got %+v (open %v), want sequence %d", e, open, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("sequence %d not delivered", want)
	}
}

func TestWake_KeepsNothingForATaskWithNoSubscriber(t *testing.T) {
	el := newTestPersistentEventLog(t)
	for i := range 100 {
		el.Wake(tid(fmt.Sprintf("wake-nosub-%d", i)))
	}
	if g, _ := peerStateLen(el); g != 0 {
		t.Fatalf("remoteGen holds %d tasks with no subscriber", g)
	}
}

func TestSweep_LeavesNoTaskStateWhenItExits(t *testing.T) {
	a := newTestPersistentEventLog(t)
	b := peerOf(a)
	taskID := tid("sweep-exit")
	appendStatus(t, a, taskID, 2)
	ctx, cancel := context.WithCancel(t.Context())
	ch, err := b.Subscribe(ctx, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	recvEvent(t, ch, 1)
	recvEvent(t, ch, 2)
	appendStatus(t, a, taskID, 1)
	b.Wake(taskID)
	recvEvent(t, ch, 3)
	if err := a.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	cancel()
	for range ch {
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		b.mu.Lock()
		running := b.sweepRunning
		b.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sweep did not exit")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if g, d := peerStateLen(b); g != 0 || d != 0 {
		t.Fatalf("after the sweep exited: remoteGen %d, storedDone %d", g, d)
	}
}

func TestWake_SkippedBeforeSubscribeLosesNoRow(t *testing.T) {
	a := newTestPersistentEventLog(t)
	b := peerOf(a)
	b.pollInterval = time.Hour
	taskID := tid("wake-skip")
	appendStatus(t, a, taskID, 1)
	b.Wake(taskID)
	ch, err := b.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	recvEvent(t, ch, 1)
	for seq := int64(2); seq <= 20; seq++ {
		appendStatus(t, a, taskID, 1)
		b.Wake(taskID)
		recvEvent(t, ch, seq)
	}
}

type maxSeqBlipRepo struct {
	sseEventStore
	fail *atomic.Bool
}

func (r maxSeqBlipRepo) GetMaxSequence(ctx context.Context, taskID string) (int, error) {
	if r.fail.Load() {
		return 0, errors.New("connection reset")
	}
	return r.sseEventStore.GetMaxSequence(ctx, taskID)
}

func TestSubscribe_FollowsTheTaskWhenItsCheckFails(t *testing.T) {
	writer := newTestPersistentEventLog(t)
	reader := peerOf(writer)
	fail := &atomic.Bool{}
	fail.Store(true)
	reader.repo = maxSeqBlipRepo{sseEventStore: writer.repo, fail: fail}
	reader.SetTaskRunningCheck(func(string) (bool, bool) { return false, false })
	taskID := tid("subscribe-blip")
	appendStatus(t, writer, taskID, 1)

	ch, err := reader.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatalf("Subscribe during a database blip: %v", err)
	}
	fail.Store(false)
	recvEvent(t, ch, 1)
	if _, err := writer.Append(t.Context(), taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	reader.Wake(taskID)
	recvEvent(t, ch, 2)
}

func TestSubscribe_EndsWhenTheLastRowWasRefused(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = rejectingBatchRepo{sseEventStore: el.repo, badSeq: 2}
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("refused-final")
	appendStatus(t, el, taskID, 1)
	if _, err := el.Append(t.Context(), taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		el.mu.Lock()
		pending := el.unflushed
		el.mu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tail was not flushed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ch, err := el.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	recvEvent(t, ch, 1)
	select {
	case e, open := <-ch:
		if open {
			t.Fatalf("got %+v, want the stream closed", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the stream waited for a row the database refused")
	}
}
