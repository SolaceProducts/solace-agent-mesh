// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func twoLogsOnOneDB(t *testing.T) (first, second *PersistentEventLog) {
	t.Helper()
	sdb := store.TestDB(t)
	mk := func() *PersistentEventLog {
		el := NewPersistentEventLog(store.NewSSEEventRepo(sdb), store.NewTaskRepo(sdb), slog.Default())
		el.pollInterval = 10 * time.Millisecond
		return el
	}
	return mk(), mk()
}

func received(brokerMessageID string, redelivered bool, acks *atomic.Int32) (context.Context, *delivery.Delivery) {
	return delivery.Attach(context.Background(), brokerMessageID, redelivered, func() { acks.Add(1) })
}

func TestTakeover_ContinuesNumberingFromStoredMaximum(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("takeover-seq")
	appendStatus(t, first, taskID, 5)

	seq, err := second.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 6 {
		t.Fatalf("second instance numbered %d, want 6: it must continue from the stored maximum", seq)
	}
	if got := dbSeqs(t, second, taskID); len(got) != 6 {
		t.Fatalf("stored %v, want 1..6 with nothing dropped", got)
	}
}

func TestTakeover_GatewayRowKeepsItsNumber(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	killed := &atomic.Bool{}
	first.repo = failAppendBatchRepo{sseEventStore: first.repo, fail: killed}
	first.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(first.Stop)
	taskID := tid("takeover-gateway-row")
	appendDelivered(t, first, taskID, 2)

	notice, err := first.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := dbSeqs(t, first, taskID); notice != 3 || len(got) != 3 {
		t.Fatalf("gateway row %d, stored %v: want row 3 stored with every row before it", notice, got)
	}
	killed.Store(true)
	appendDelivered(t, first, taskID, 1)

	var acks atomic.Int32
	ctx, d := received("m4", true, &acks)
	defer d.Release()
	seq, err := second.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 4 {
		t.Fatalf("redelivered event numbered %d, want 4: the number the first instance sent", seq)
	}
}

func TestTakeover_CompletedTaskStaysCompleted(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	ctx := t.Context()
	taskID := tid("takeover-done")
	appendStatus(t, first, taskID, 2)
	if err := first.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}

	_, err := second.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if !errors.Is(err, ErrTaskCompleted) {
		t.Fatalf("late event on a completed task: err = %v, want ErrTaskCompleted", err)
	}
}

func TestAck_SyncWaitsForInsertAndHandler(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)

	if _, err := el.Append(ctx, tid("ack-sync"), tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if acks.Load() != 0 {
		t.Fatal("acknowledged before the handler finished")
	}
	d.Release()
	if acks.Load() != 1 {
		t.Fatalf("acks = %d, want 1 once the row is written and the handler is done", acks.Load())
	}
}

func TestAck_AsyncWaitsForTheFlush(t *testing.T) {
	el := newTestPersistentEventLog(t)
	wrap := &failFromBatchRepo{sseEventStore: el.repo}
	wrap.failFrom.Store(1)
	el.repo = wrap
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)

	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	taskID := tid("ack-async")
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()

	waitFor(t, func() bool { return wrap.calls.Load() >= 2 })
	if acks.Load() != 0 {
		t.Fatal("acknowledged while the row was not written")
	}
	wrap.failFrom.Store(0)
	waitFor(t, func() bool { return acks.Load() == 1 })
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want one row", got)
	}
}

func TestAck_FinalRowWrittenStillWaitsForHandler(t *testing.T) {
	el := newTestPersistentEventLogAsync(t)
	var acks atomic.Int32
	ctx, d := received("m-final", false, &acks)
	taskID := tid("ack-final")
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if acks.Load() != 0 {
		t.Fatal("acknowledged when the row was written but before the handler finished its completion work")
	}
	d.Release()
	waitFor(t, func() bool { return acks.Load() == 1 })
}

func TestRedelivered_StoredRowIsNotStoredAgain(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("redeliver-db")
	var acks atomic.Int32

	ctx, d := received("m1", false, &acks)
	if _, err := first.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()

	ctx2, d2 := received("m1", true, &acks)
	seq, err := second.Append(ctx2, taskID, tid("sess"), "u", "status_update", []byte(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("redelivered event got seq %d, want its stored 1", seq)
	}
	d2.Release()
	if got := dbSeqs(t, second, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want the one row", got)
	}
	if acks.Load() != 2 {
		t.Fatalf("acks = %d, want both deliveries acknowledged", acks.Load())
	}

	seq, err = second.Append(t.Context(), taskID, tid("sess"), "u", "status_update", []byte(`{"n":2}`))
	if err != nil || seq != 2 {
		t.Fatalf("next event seq = %d, %v; want 2", seq, err)
	}
}

func TestRedelivered_NotStoredYetIsStored(t *testing.T) {
	_, second := twoLogsOnOneDB(t)
	taskID := tid("redeliver-new")
	var acks atomic.Int32
	ctx, d := received("m9", true, &acks)
	seq, err := second.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil || seq != 1 {
		t.Fatalf("seq = %d, %v; want 1", seq, err)
	}
	d.Release()
	if got := dbSeqs(t, second, taskID); len(got) != 1 || acks.Load() != 1 {
		t.Fatalf("stored %v, acks %d; want one row and one acknowledgement", got, acks.Load())
	}
}

func TestRedelivered_RowStillInTailWaitsForTheFlush(t *testing.T) {
	el := newTestPersistentEventLog(t)
	real := el.repo
	fail := &atomic.Bool{}
	fail.Store(true)
	el.repo = failAppendBatchRepo{sseEventStore: real, fail: fail}
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)

	taskID := tid("redeliver-tail")
	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()

	ctx2, d2 := received("m1", true, &acks)
	seq, err := el.Append(ctx2, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil || seq != 1 {
		t.Fatalf("seq = %d, %v; want the tail row's 1", seq, err)
	}
	d2.Release()
	if acks.Load() != 0 {
		t.Fatal("the redelivered copy was acknowledged before the row was written")
	}
	fail.Store(false)
	waitFor(t, func() bool { return acks.Load() == 2 })
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want one row", got)
	}
}

type failingStateRepo struct {
	sseEventStore
	failures *atomic.Int32
}

func (r failingStateRepo) SequenceState(ctx context.Context, taskID string) (int, bool, error) {
	if r.failures.Add(-1) >= 0 {
		return 0, false, errors.New("simulated DB read failure")
	}
	return r.sseEventStore.SequenceState(ctx, taskID)
}

func TestReadFailure_ReceivedEventIsRetried(t *testing.T) {
	el := newTestPersistentEventLog(t)
	failures := &atomic.Int32{}
	failures.Store(1)
	el.repo = failingStateRepo{sseEventStore: el.repo, failures: failures}

	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	seq, err := el.Append(ctx, tid("read-retry"), tid("sess"), "u", "status_update", []byte(`{}`))
	if err != nil || seq != 1 {
		t.Fatalf("seq = %d, %v; want 1 after the retry", seq, err)
	}
	d.Release()
	if acks.Load() != 1 {
		t.Fatalf("acks = %d, want 1", acks.Load())
	}
}

func TestReadFailure_StoppingLeavesTheMessageUnacknowledged(t *testing.T) {
	el := newTestPersistentEventLog(t)
	failures := &atomic.Int32{}
	failures.Store(1000)
	el.repo = failingStateRepo{sseEventStore: el.repo, failures: failures}

	var acks atomic.Int32
	base, cancel := context.WithCancel(t.Context())
	ctx, d := delivery.Attach(base, "m1", false, func() { acks.Add(1) })
	time.AfterFunc(20*time.Millisecond, cancel)
	if _, err := el.Append(ctx, tid("read-stop"), tid("sess"), "u", "status_update", []byte(`{}`)); err == nil {
		t.Fatal("Append succeeded with the database unreadable")
	}
	d.Release()
	if acks.Load() != 0 {
		t.Fatal("a message whose event was not stored was acknowledged")
	}
}

func TestReadFailure_OwnRowIsNotRetried(t *testing.T) {
	el := newTestPersistentEventLog(t)
	failures := &atomic.Int32{}
	failures.Store(1000)
	el.repo = failingStateRepo{sseEventStore: el.repo, failures: failures}

	done := make(chan error, 1)
	go func() {
		_, err := el.Append(t.Context(), tid("read-own"), tid("sess"), "u", "status_update", []byte(`{}`))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Append succeeded with the database unreadable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a row the gateway writes on its own blocked on retries")
	}
}

func TestCleanup_ReleasesHeldAcknowledgements(t *testing.T) {
	el := newTestPersistentEventLogTailOnly(t)
	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	taskID := tid("cleanup-acks")
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()
	if err := el.Cleanup(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if acks.Load() != 1 {
		t.Fatalf("acks = %d, want the removed row's message acknowledged", acks.Load())
	}
}

func TestSubscribe_WaitsForARunningTaskWithNoEvent(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("subscribe-early")
	var running, known atomic.Bool
	running.Store(true)
	known.Store(true)
	second.SetTaskRunningCheck(func(string) (bool, bool) { return running.Load(), known.Load() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ch, err := second.Subscribe(ctx, taskID, 0)
	if err != nil {
		t.Fatalf("Subscribe before the first event of a running task: %v", err)
	}
	appendStatus(t, first, taskID, 1)
	select {
	case evt := <-ch:
		if evt.Sequence != 1 {
			t.Fatalf("got seq %d, want 1", evt.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stored event never reached the waiting subscriber")
	}

	running.Store(false)
	if _, err = second.Subscribe(ctx, tid("subscribe-unknown"), 0); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("task not running: err = %v, want ErrTaskNotFound", err)
	}
	known.Store(false)
	unread, err := second.Subscribe(ctx, tid("subscribe-unread"), 0)
	if err != nil {
		t.Fatalf("task state unreadable: err = %v, want the task followed", err)
	}
	known.Store(true)
	select {
	case _, open := <-unread:
		if open {
			t.Fatal("a task that is not running delivered an event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream on a task that is not running did not close once its state was readable")
	}
}

type hangingBatchRepo struct {
	sseEventStore
	hung *atomic.Bool
}

func (r hangingBatchRepo) AppendBatch(ctx context.Context, rows []*store.SSEEventBufferRow) (int64, error) {
	if r.hung.CompareAndSwap(false, true) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(5 * time.Second):
			return 0, errors.New("hung call released by the test")
		}
	}
	return r.sseEventStore.AppendBatch(ctx, rows)
}

func TestFlush_HungCallIsAbandonedAtTheDeadline(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.dbCallTimeout = 100 * time.Millisecond
	el.repo = hangingBatchRepo{sseEventStore: el.repo, hung: &atomic.Bool{}}
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)

	var acks atomic.Int32
	ctx, d := received("m1", false, &acks)
	taskID := tid("hung-flush")
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()
	start := time.Now()
	waitFor(t, func() bool { return acks.Load() == 1 })
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("acknowledged after %v; the hung flush was not abandoned at its deadline", waited)
	}
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want one row", got)
	}
}
