// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func dbSeqs(t *testing.T, el *PersistentEventLog, taskID string) []int {
	t.Helper()
	rows, err := el.repo.GetByTask(context.Background(), taskID, 0)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	seqs := make([]int, len(rows))
	for i, r := range rows {
		seqs[i] = r.EventSequence
	}
	return seqs
}

func appendStatus(t *testing.T, el *PersistentEventLog, taskID string, n int) {
	t.Helper()
	ctx := context.Background()
	for range n {
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"k":1}`)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func appendDelivered(t *testing.T, el *PersistentEventLog, taskID string, n int) {
	t.Helper()
	for range n {
		ctx, d := delivery.Attach(context.Background(), "", false, func() {})
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"k":1}`)); err != nil {
			t.Fatalf("Append: %v", err)
		}
		d.Release()
	}
}

func contiguousFrom1(seqs []int64) bool {
	for i, s := range seqs {
		if s != int64(i+1) {
			return false
		}
	}
	return true
}

func TestAsync_ReplayMergesUnflushedTail(t *testing.T) {
	el := newTestPersistentEventLogAsync(t)
	taskID := tid("merge")

	appendStatus(t, el, taskID, 20)
	if got := replaySeqs(t, el, taskID, 1); !equalSeqsLen(got, 20) || !contiguousFrom1(got) {
		t.Fatalf("pre-flush Replay = %v, want contiguous 1..20", got)
	}

	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 20 })
	if got := replaySeqs(t, el, taskID, 1); !equalSeqsLen(got, 20) || !contiguousFrom1(got) {
		t.Fatalf("post-flush Replay = %v, want contiguous 1..20", got)
	}

	if got := replaySeqs(t, el, taskID, 15); !equalSeqs(got, 15, 16, 17, 18, 19, 20) {
		t.Fatalf("Replay from 15 = %v", got)
	}
}

func TestAsync_StopDrains(t *testing.T) {
	el := newTestPersistentEventLogAsync(t)
	taskID := tid("stop")
	appendStatus(t, el, taskID, 30)
	el.Stop()
	if got := dbSeqs(t, el, taskID); len(got) != 30 {
		t.Fatalf("after Stop, DB has %d rows, want 30", len(got))
	}
}

func TestAsync_EmptyKeyDoesNotPoisonBatch(t *testing.T) {
	el := newTestPersistentEventLogAsync(t)
	ctx := context.Background()
	taskID := tid("poison")

	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"n":1}`)); err != nil {
		t.Fatalf("valid 1: %v", err)
	}
	if seq, err := el.Append(ctx, taskID, "", "u", "status_update", []byte(`{"empty":true}`)); err != nil || seq != 0 {
		t.Fatalf("empty session: got (%d, %v), want (0, nil)", seq, err)
	}
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"n":2}`)); err != nil {
		t.Fatalf("valid 2: %v", err)
	}

	if got := dbSeqs(t, el, taskID); len(got) != 2 {
		t.Fatalf("expected 2 valid rows persisted, got %d (%v)", len(got), got)
	}
}

func TestAsync_OverCapStaysBounded(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(20, time.Hour, 50)
	t.Cleanup(el.Stop)
	taskID := tid("cap")

	appendStatus(t, el, taskID, 500)
	el.Stop()
	if got := dbSeqs(t, el, taskID); len(got) != 500 {
		t.Fatalf("over-cap run lost events: DB has %d, want 500", len(got))
	}
}

func TestAsync_FindSequenceAfterTimestampTail(t *testing.T) {
	el := newTestPersistentEventLogTailOnly(t)
	ctx := context.Background()
	taskID := tid("ts")

	appendStatus(t, el, taskID, 3)
	time.Sleep(15 * time.Millisecond)
	boundary := time.Now().UnixMilli()
	time.Sleep(15 * time.Millisecond)
	appendStatus(t, el, taskID, 2)

	seq, err := el.FindSequenceAfterTimestamp(ctx, taskID, boundary)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 4 {
		t.Errorf("FindSequenceAfterTimestamp = %d, want 4 (first event after boundary, from tail)", seq)
	}

	if seq, _ := el.FindSequenceAfterTimestamp(ctx, taskID, time.Now().UnixMilli()+1000); seq != 6 {
		t.Errorf("FindSequenceAfterTimestamp(future) = %d, want 6 (next seq)", seq)
	}
}

func TestAsync_FindLastEventByDataTypeTail(t *testing.T) {
	el := newTestPersistentEventLogTailOnly(t)
	ctx := context.Background()
	taskID := tid("hil")

	appendStatus(t, el, taskID, 2)
	hil := []byte(`{"result":{"kind":"status-update","status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request"}}]}}}}`)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", hil); err != nil {
		t.Fatal(err)
	}

	got := el.FindLastEventByDataType(ctx, taskID, "user_input_request")
	if got == nil {
		t.Fatal("expected to find the user_input_request marker in the tail")
	}
}

func TestAsync_CleanupClearsTail(t *testing.T) {
	el := newTestPersistentEventLogAsync(t)
	ctx := context.Background()
	taskID := tid("cleanup")
	appendStatus(t, el, taskID, 5)

	if err := el.Cleanup(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if got := replaySeqs(t, el, taskID, 1); len(got) != 0 {
		t.Errorf("after Cleanup, Replay = %v, want empty", got)
	}
}

func newTestPersistentEventLogTailOnly(t *testing.T) *PersistentEventLog {
	t.Helper()
	el := newTestPersistentEventLog(t)
	fail := &atomic.Bool{}
	fail.Store(true)
	el.repo = failAppendBatchRepo{sseEventStore: el.repo, fail: fail}
	el.EnableAsyncFlush(50, time.Hour, 100000)
	t.Cleanup(el.Stop)
	return el
}

type failAppendBatchRepo struct {
	sseEventStore
	fail *atomic.Bool
}

func (r failAppendBatchRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	if r.fail.Load() {
		return 0, errors.New("simulated DB write failure")
	}
	return r.sseEventStore.AppendBatch(ctx, rows)
}

func TestAsync_FlushFailureRetainsThenDrops(t *testing.T) {
	el := newTestPersistentEventLog(t)
	real := el.repo
	fail := &atomic.Bool{}
	fail.Store(true)
	el.repo = failAppendBatchRepo{sseEventStore: real, fail: fail}
	el.EnableAsyncFlush(20, time.Hour, 50)
	t.Cleanup(el.Stop)
	ctx := context.Background()
	taskID := tid("faildrop")

	for range 60 {
		_, _ = el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"k":1}`))
	}
	if got := replaySeqs(t, el, taskID, 1); !contiguousFrom1(got) || len(got) != 60 {
		t.Fatalf("during DB failure, Replay should serve the full tail; got %d events", len(got))
	}

	for range 200 {
		_, _ = el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"k":1}`))
	}
	el.mu.Lock()
	unflushed := el.unflushed
	el.mu.Unlock()
	if unflushed > 2*el.maxUnflushed {
		t.Fatalf("drop valve did not bound the tail: unflushed=%d, want <= %d", unflushed, 2*el.maxUnflushed)
	}

	fail.Store(false)
	el.Stop()
	if got := dbSeqs(t, el, taskID); len(got) == 0 {
		t.Fatal("after recovery + drain, expected some events persisted")
	}
}

type flushOnGetRepo struct {
	sseEventStore
	once sync.Once
	hook func()
}

func (r *flushOnGetRepo) GetByTask(ctx context.Context, taskID string, from int) ([]*store.SSEEventBufferRow, error) {
	rows, err := r.sseEventStore.GetByTask(ctx, taskID, from)
	r.once.Do(func() {
		if r.hook != nil {
			r.hook()
		}
	})
	return rows, err
}

func TestAsync_ReplayNoGapWhenFlushRacesDBRead(t *testing.T) {
	el := newTestPersistentEventLog(t)
	wrap := &flushOnGetRepo{sseEventStore: el.repo}
	el.repo = wrap
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	ctx := context.Background()
	taskID := tid("toctou")

	appendStatus(t, el, taskID, 10)
	appendDelivered(t, el, taskID, 5)
	wrap.hook = func() { el.flushOnce(ctx, false) }

	got := replaySeqs(t, el, taskID, 1)
	if !contiguousFrom1(got) || len(got) != 15 {
		t.Fatalf("Replay gapped when a flush raced the DB read: got %v, want contiguous 1..15", got)
	}
}

func TestAsync_FindLastEventByDataTypeAcrossBoundary(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	ctx := context.Background()

	mark := func(tag string) []byte {
		return []byte(`{"result":{"kind":"status-update","status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","tag":"` + tag + `"}}]}}}}`)
	}

	taskA := tid("hilA")
	if _, err := el.Append(ctx, taskA, tid("s"), "u", "status_update", mark("old")); err != nil {
		t.Fatal(err)
	}
	flushNow(ctx, el)
	if _, err := el.Append(ctx, taskA, tid("s"), "u", "status_update", mark("new")); err != nil {
		t.Fatal(err)
	}
	if got := el.FindLastEventByDataType(ctx, taskA, "user_input_request"); !bytes.Contains(got, []byte(`"tag":"new"`)) {
		t.Errorf("expected the newer tail marker, got %s", got)
	}

	taskB := tid("hilB")
	if _, err := el.Append(ctx, taskB, tid("s"), "u", "status_update", mark("only")); err != nil {
		t.Fatal(err)
	}
	flushNow(ctx, el)
	appendStatus(t, el, taskB, 3)
	if got := el.FindLastEventByDataType(ctx, taskB, "user_input_request"); !bytes.Contains(got, []byte(`"tag":"only"`)) {
		t.Errorf("expected the DB marker via fallback, got %s", got)
	}
}

func TestAsync_UnflushedTailNotYetDurable(t *testing.T) {
	el := newTestPersistentEventLogTailOnly(t)
	taskID := tid("crashwin")
	appendStatus(t, el, taskID, 8)

	if got := replaySeqs(t, el, taskID, 1); len(got) != 8 {
		t.Fatalf("Replay should serve the tail: got %d, want 8", len(got))
	}
	if got := dbSeqs(t, el, taskID); len(got) != 0 {
		t.Fatalf("events must NOT be durable before a flush: DB has %d, want 0", len(got))
	}
}

func TestAsync_FailedFlushRetainsTheTailAcrossComplete(t *testing.T) {
	el := newTestPersistentEventLog(t)
	fail := &atomic.Bool{}
	fail.Store(true)
	el.repo = failAppendBatchRepo{sseEventStore: el.repo, fail: fail}
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	ctx := context.Background()
	taskID := tid("completefail")

	appendStatus(t, el, taskID, 6)
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if got := replaySeqs(t, el, taskID, 1); len(got) != 6 {
		t.Fatalf("after the failed flush, Replay = %d, want 6 (tail retained)", len(got))
	}
	fail.Store(false)
	el.Stop()
	if got := dbSeqs(t, el, taskID); len(got) != 6 {
		t.Fatalf("after recovery + drain, DB has %d, want 6", len(got))
	}
}

func equalSeqsLen(s []int64, n int) bool { return len(s) == n }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("condition not met within timeout")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestAsync_FlushDrainsPastDuplicatePK(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var buf bytes.Buffer
	el.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.Background()
	task, sess := tid("dup-flush"), tid("sess")

	if _, err := el.repo.AppendBatch(ctx, []*store.SSEEventBufferRow{{
		TaskID: task, SessionID: sess, UserID: "u", EventSequence: 1,
		EventType: "status_update", EventData: `{"run":1}`, CreatedAt: 1,
	}}); err != nil {
		t.Fatalf("seed prior-run row: %v", err)
	}

	el.async = true
	el.flushBatchSize = 50
	el.tail = map[string][]pendingEvent{task: {
		{seq: 1, sessionID: sess, userID: "u", eventType: "status_update", payload: []byte(`{"run":2}`), createdAt: 2},
		{seq: 2, sessionID: sess, userID: "u", eventType: "status_update", payload: []byte(`{"run":2}`), createdAt: 2},
		{seq: 3, sessionID: sess, userID: "u", eventType: "final_response", payload: []byte(`{"run":2}`), createdAt: 2},
	}}
	el.unflushed = 3

	el.flushOnce(ctx, false)

	el.mu.Lock()
	unflushed, tailLen := el.unflushed, len(el.tail[task])
	el.mu.Unlock()
	if unflushed != 0 || tailLen != 0 {
		t.Fatalf("flusher wedged on the duplicate: unflushed=%d tailLen=%d (want 0,0)", unflushed, tailLen)
	}

	rows, err := el.repo.GetByTask(ctx, task, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows (seq 1 original + 2,3 fresh), got %d", len(rows))
	}
	if rows[0].EventData != `{"run":1}` {
		t.Errorf("seq 1 should keep the original run-1 content, got %s", rows[0].EventData)
	}

	if !strings.Contains(buf.String(), "skipped already-durable duplicate rows on flush") {
		t.Errorf("expected a dup-skip WARN, log was:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), task) {
		t.Errorf("dup-skip WARN should name the candidate task %s, log was:\n%s", task, buf.String())
	}
}

type countingAppendBatchRepo struct {
	sseEventStore
	batches *atomic.Int32
}

func (r countingAppendBatchRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	r.batches.Add(1)
	return r.sseEventStore.AppendBatch(ctx, rows)
}

func TestAsync_FlushGathersAppendsUntilIntervalOrFullBatch(t *testing.T) {
	el := newTestPersistentEventLog(t)
	batches := &atomic.Int32{}
	el.repo = countingAppendBatchRepo{sseEventStore: el.repo, batches: batches}
	const interval = 10 * time.Second
	el.EnableAsyncFlush(10, interval, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("debounce")

	appendDelivered(t, el, taskID, 1)
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if n := batches.Load(); n != 1 {
		t.Fatalf("first event took %d commits, want 1", n)
	}

	appendDelivered(t, el, taskID, 4)
	time.Sleep(100 * time.Millisecond)
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("partial batch persisted before the interval: %v", got)
	}
	if got := replaySeqs(t, el, taskID, 1); !equalSeqsLen(got, 5) || !contiguousFrom1(got) {
		t.Fatalf("Replay before flush = %v, want contiguous 1..5 from the tail", got)
	}
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 5 })
	if n := batches.Load(); n != 2 {
		t.Fatalf("first event plus 4 appends took %d commits, want 2", n)
	}

	full := tid("fullbatch")
	appendDelivered(t, el, full, 1)
	waitFor(t, func() bool { return len(dbSeqs(t, el, full)) == 1 })
	appendDelivered(t, el, full, 10)
	waitFor(t, func() bool { return len(dbSeqs(t, el, full)) == 11 })
}

func TestAsync_WaitingSubscriberIsServedWithoutWaitingForTheInterval(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(100, 10*time.Second, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("waiting-subscriber")

	appendStatus(t, el, taskID, 1)
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	ch, err := el.Subscribe(ctx, taskID, 1)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case evt := <-ch:
		if evt.Sequence != 1 {
			t.Fatalf("first delivered seq = %d, want 1", evt.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("durable first event was not delivered")
	}

	appendStatus(t, el, taskID, 1)
	select {
	case evt := <-ch:
		if evt.Sequence != 2 {
			t.Fatalf("next delivered seq = %d, want 2", evt.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiting subscriber did not receive the new event before the flush interval")
	}
}

func TestAsync_NonPositiveIntervalUsesTheDefault(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(100, -time.Second, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("negative-interval")

	appendStatus(t, el, taskID, 2)
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 2 })
}

func flushNow(ctx context.Context, el *PersistentEventLog) {
	el.flushOnce(ctx, false)
}
