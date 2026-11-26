// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func counter(n int32) *atomic.Int32 {
	c := &atomic.Int32{}
	c.Store(n)
	return c
}

type failingWriteRepo struct {
	sseEventStore
	appendFailures *atomic.Int32
	lookupFailures *atomic.Int32
}

func (r failingWriteRepo) Append(ctx context.Context, row *store.SSEEventBufferRow) error {
	if r.appendFailures.Add(-1) >= 0 {
		return errors.New("simulated DB write failure")
	}
	return r.sseEventStore.Append(ctx, row)
}

func (r failingWriteRepo) BrokerMessageSequence(ctx context.Context, taskID, brokerMessageID string) (int, error) {
	if r.lookupFailures.Add(-1) >= 0 {
		return 0, errors.New("simulated DB read failure")
	}
	return r.sseEventStore.BrokerMessageSequence(ctx, taskID, brokerMessageID)
}

func TestWriteFailure_ReceivedEventIsRetriedInSyncMode(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = failingWriteRepo{sseEventStore: el.repo, appendFailures: counter(1), lookupFailures: counter(1)}

	var acks atomic.Int32
	ctx, d := received("m1", true, &acks)
	taskID := tid("write-retry")
	seq, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil || seq != 1 {
		t.Fatalf("seq = %d, %v; want 1 after the retries", seq, err)
	}
	d.Release()
	if acks.Load() != 1 {
		t.Fatalf("acks = %d, want 1", acks.Load())
	}
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want one row", got)
	}
}

func TestWriteFailure_StoppingLeavesTheMessageUnacknowledged(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = failingWriteRepo{sseEventStore: el.repo, appendFailures: counter(1000), lookupFailures: counter(0)}

	var acks atomic.Int32
	base, cancel := context.WithCancel(t.Context())
	ctx, d := delivery.Attach(base, "m1", false, func() { acks.Add(1) })
	time.AfterFunc(20*time.Millisecond, cancel)
	taskID := tid("write-stop")
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err == nil {
		t.Fatal("Append succeeded with the database unwritable")
	}
	d.Release()
	if acks.Load() != 0 {
		t.Fatal("the message was acknowledged with no row stored")
	}
	el.mu.Lock()
	seq := el.sequences[taskID]
	el.mu.Unlock()
	if seq != 0 {
		t.Fatalf("sequence %d was not rolled back", seq)
	}
}

type rejectingStateRepo struct{ sseEventStore }

func (rejectingStateRepo) SequenceState(context.Context, string) (int, bool, error) {
	return 0, false, &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"}
}

func TestRejected_ReceivedEventIsDroppedNotRetried(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = rejectingStateRepo{el.repo}

	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	start := time.Now()
	if _, err := el.Append(ctx, tid("rejected"), tid("sess"), "u", "status_update", []byte(`{}`)); !errors.Is(err, ErrEventRejected) {
		t.Fatalf("err = %v, want ErrEventRejected", err)
	}
	if waited := time.Since(start); waited >= readRetryMin {
		t.Fatalf("a rejected event was retried (%v)", waited)
	}
	d.Release()
	if acks.Load() != 1 {
		t.Fatalf("acks = %d, want 1", acks.Load())
	}
}

type rejectingBatchRepo struct {
	sseEventStore
	badSeq int
}

func (r rejectingBatchRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	for _, row := range rows {
		if row.EventSequence == r.badSeq {
			return 0, &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"}
		}
	}
	return r.sseEventStore.AppendBatch(ctx, rows)
}

func (r rejectingBatchRepo) Append(ctx context.Context, row *store.SSEEventBufferRow) error {
	if row.EventSequence == r.badSeq {
		return &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"}
	}
	return r.sseEventStore.Append(ctx, row)
}

func TestFlush_RejectedRowIsDroppedAndTheRestWritten(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = rejectingBatchRepo{sseEventStore: el.repo, badSeq: 2}
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)

	var acks atomic.Int32
	taskID := tid("rejected-row")
	for _, id := range []string{"m1", "m2", "m3"} {
		ctx, d := received(id, false, &acks)
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		d.Release()
	}
	waitFor(t, func() bool { return acks.Load() == 3 })
	if got := dbSeqs(t, el, taskID); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("stored %v, want [1 3]", got)
	}
}

func TestSeed_ForgetKeepsNumberingAboveTheTail(t *testing.T) {
	el := newTestPersistentEventLogTailOnly(t)
	taskID := tid("forget-tail")
	appendStatus(t, el, taskID, 3)
	el.Forget(taskID)

	seq, err := el.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil || seq != 4 {
		t.Fatalf("seq = %d, %v; want 4 above the three rows still in the tail", seq, err)
	}
}

func TestSubscribe_ClosesWhenTheTaskEndsWithNoRow(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var running atomic.Bool
	running.Store(true)
	el.SetTaskRunningCheck(func(string) (bool, bool) { return running.Load(), true })

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ch, err := el.Subscribe(ctx, tid("no-row"), 0)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * el.pollInterval)
	running.Store(false)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("got an event for a task with no row")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream stayed open after the task ended")
	}
}

func TestSeed_ConcurrentFirstWritersGetUniqueSequences(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("concurrent-seed")
	appendStatus(t, first, taskID, 5)

	const n = 8
	seqs := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq, err := second.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`))
			if err != nil {
				t.Error(err)
				return
			}
			seqs[i] = int(seq)
		}()
	}
	wg.Wait()
	sort.Ints(seqs)
	for i, s := range seqs {
		if s != 6+i {
			t.Fatalf("sequences %v, want 6 to %d once each", seqs, 5+n)
		}
	}
}

type failFromBatchRepo struct {
	sseEventStore
	calls    atomic.Int32
	failFrom atomic.Int32
}

func (r *failFromBatchRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	n := r.calls.Add(1)
	if from := r.failFrom.Load(); from > 0 && n >= from {
		return 0, errors.New("simulated DB write failure")
	}
	return r.sseEventStore.AppendBatch(ctx, rows)
}

func TestAck_PartialFlushReleasesOnlyWrittenRows(t *testing.T) {
	el := newTestPersistentEventLog(t)
	wrap := &failFromBatchRepo{sseEventStore: el.repo}
	wrap.failFrom.Store(2)
	el.repo = wrap
	el.async = true
	el.flushBatchSize = 1

	var acks atomic.Int32
	ack := func() { acks.Add(1) }
	taskID, sess := tid("partial-flush"), tid("sess")
	row := func(seq int) pendingEvent {
		return pendingEvent{seq: seq, sessionID: sess, userID: "u", eventType: "status_update",
			payload: []byte(`{}`), createdAt: int64(seq), acks: []func(){ack}}
	}
	el.tail = map[string][]pendingEvent{taskID: {row(1), row(2), row(3)}}
	el.unflushed = 3
	el.sequences[taskID] = 3

	ctx := t.Context()
	el.flushOnce(ctx, false)
	if acks.Load() != 1 {
		t.Fatalf("acks = %d after a partial flush, want 1", acks.Load())
	}
	wrap.failFrom.Store(0)
	el.flushOnce(ctx, false)
	if acks.Load() != 3 {
		t.Fatalf("acks = %d after the full flush, want 3", acks.Load())
	}
	if got := dbSeqs(t, el, taskID); len(got) != 3 {
		t.Fatalf("stored %v, want three rows", got)
	}
}

type serverStateRepo struct {
	sseEventStore
	batchFailures  *atomic.Int32
	appendFailures *atomic.Int32
}

func demoted() error {
	return &pgconn.PgError{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"}
}

func (r serverStateRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	if r.batchFailures.Add(-1) >= 0 {
		return 0, demoted()
	}
	return r.sseEventStore.AppendBatch(ctx, rows)
}

func (r serverStateRepo) Append(ctx context.Context, row *store.SSEEventBufferRow) error {
	if r.appendFailures.Add(-1) >= 0 {
		return demoted()
	}
	return r.sseEventStore.Append(ctx, row)
}

func TestServerStateError_KeepsTheHoldAndStoresOnRetry(t *testing.T) {
	t.Run("async", func(t *testing.T) {
		el := newTestPersistentEventLog(t)
		el.repo = serverStateRepo{sseEventStore: el.repo, batchFailures: counter(1), appendFailures: counter(1000)}
		el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
		t.Cleanup(el.Stop)
		var acks atomic.Int32
		ctx, d := received("m1", false, &acks)
		taskID := tid("demoted-async")
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		d.Release()
		waitFor(t, func() bool { return acks.Load() == 1 })
		if got := dbSeqs(t, el, taskID); len(got) != 1 {
			t.Fatalf("stored %v, want one row", got)
		}
	})
	t.Run("sync", func(t *testing.T) {
		el := newTestPersistentEventLog(t)
		el.repo = serverStateRepo{sseEventStore: el.repo, batchFailures: counter(0), appendFailures: counter(1)}
		var acks atomic.Int32
		ctx, d := received("m1", false, &acks)
		taskID := tid("demoted-sync")
		if seq, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil || seq != 1 {
			t.Fatalf("seq = %d, %v; want 1 after the retry", seq, err)
		}
		d.Release()
		if acks.Load() != 1 || len(dbSeqs(t, el, taskID)) != 1 {
			t.Fatalf("acks = %d, rows %v", acks.Load(), dbSeqs(t, el, taskID))
		}
	})
}

func TestRejected_SyncInsertReleasesTheHold(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = rejectingBatchRepo{sseEventStore: el.repo, badSeq: 1}
	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	if _, err := el.Append(ctx, tid("rejected-sync"), tid("sess"), "u", "status_update", []byte(`{}`)); err == nil {
		t.Fatal("Append succeeded on a rejected row")
	}
	d.Release()
	if acks.Load() != 1 {
		t.Fatalf("acks = %d, want 1", acks.Load())
	}
}

func TestComplete_MarksCompletedOnlyAfterTheFinalRowIsWritten(t *testing.T) {
	el := newTestPersistentEventLog(t)
	wrap := &failFromBatchRepo{sseEventStore: el.repo}
	wrap.failFrom.Store(1)
	el.repo = wrap
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)

	taskID := tid("complete-unflushed")
	ctx := t.Context()
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if done, err := el.repo.IsCompleted(ctx, taskID); err != nil || done {
		t.Fatalf("completed = %v, %v; want not completed while the final row is unwritten", done, err)
	}
	el.Forget(taskID)
	wrap.failFrom.Store(0)
	waitFor(t, func() bool {
		done, _ := el.repo.IsCompleted(ctx, taskID)
		return done
	})
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want the final row", got)
	}
}

func TestOwnRow_SeedReadIsRetriedAFewTimes(t *testing.T) {
	el := newTestPersistentEventLog(t)
	failures := &atomic.Int32{}
	failures.Store(1)
	el.repo = failingStateRepo{sseEventStore: el.repo, failures: failures}
	taskID := tid("own-row-seed")
	seq, err := el.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil || seq != 1 {
		t.Fatalf("seq = %d, %v; want 1 after one failed seed read", seq, err)
	}
}
