// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestRedelivered_FoundAfterTheBackgroundFlush(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	first.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(first.Stop)
	taskID := tid("redelivered-flush")
	var acks atomic.Int32

	ctx, d := received("m1", false, &acks)
	if _, err := first.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	d.Release()
	waitFor(t, func() bool { return acks.Load() == 1 })

	ctx, d = received("m1", true, &acks)
	seq, err := second.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	d.Release()
	if err != nil || seq != 1 {
		t.Fatalf("redelivered copy: seq %d err %v, want the stored 1", seq, err)
	}
	if got := dbSeqs(t, second, taskID); len(got) != 1 {
		t.Fatalf("stored %v, want the one row", got)
	}
}

func TestRedelivered_FoundAfterTheFlushCompleteWakes(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	first.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(first.Stop)
	taskID := tid("redelivered-complete")
	var acks atomic.Int32

	for i, id := range []string{"m1", "m-final"} {
		ctx, d := received(id, false, &acks)
		if _, err := first.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := first.Complete(ctx, taskID); err != nil {
				t.Fatal(err)
			}
		}
		d.Release()
		waitFor(t, func() bool { return acks.Load() == int32(i+1) })
	}

	ctx, d := received("m-final", true, &acks)
	seq, err := second.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
	d.Release()
	if err != nil || seq != 2 {
		t.Fatalf("redelivered final: seq %d err %v, want the stored 2", seq, err)
	}
	if got := dbSeqs(t, second, taskID); len(got) != 2 {
		t.Fatalf("stored %v, want rows 1 and 2", got)
	}
}

func TestRedelivered_StoredRowAboveTheCounterRaisesIt(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("redelivered-raise")
	var acks atomic.Int32
	appendReceived := func(el *PersistentEventLog, id string, redelivered bool) int64 {
		t.Helper()
		ctx, d := received(id, redelivered, &acks)
		defer d.Release()
		seq, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}

	appendReceived(first, "m1", false)
	if seq := appendReceived(second, "m2", false); seq != 2 {
		t.Fatalf("second instance numbered %d, want 2", seq)
	}
	for seq, id := range map[int]string{3: "m3", 4: "m4"} {
		if err := first.repo.Append(t.Context(), &store.SSEEventBufferRow{
			TaskID: taskID, SessionID: tid("sess"), UserID: "u", EventSequence: seq,
			EventType: "status_update", EventData: `{}`, BrokerMessageID: id,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if seq := appendReceived(second, "m4", true); seq != 4 {
		t.Fatalf("redelivered m4 numbered %d, want the stored 4", seq)
	}
	if seq := appendReceived(second, "m5", false); seq != 5 {
		t.Fatalf("next event numbered %d, want 5", seq)
	}
	if got := dbSeqs(t, second, taskID); len(got) != 5 {
		t.Fatalf("stored %v, want 1..5", got)
	}
}

type hangingReadRepo struct {
	sseEventStore
	calls atomic.Int32
}

func (r *hangingReadRepo) SequenceState(ctx context.Context, _ string) (int, bool, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return 0, false, ctx.Err()
}

func (r *hangingReadRepo) BrokerMessageSequence(ctx context.Context, _, _ string) (int, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return 0, ctx.Err()
}

func TestRetry_HungReadIsAbandonedAtTheDeadline(t *testing.T) {
	for _, tc := range []struct {
		name        string
		redelivered bool
	}{
		{"seed read", false},
		{"redelivery lookup", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			el := newTestPersistentEventLog(t)
			el.dbCallTimeout = 50 * time.Millisecond
			repo := &hangingReadRepo{sseEventStore: el.repo}
			el.repo = repo

			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			var acks atomic.Int32
			ctx, d := delivery.Attach(base, "m1", tc.redelivered, func() { acks.Add(1) })
			done := make(chan error, 1)
			go func() {
				_, err := el.Append(ctx, tid("hung-read"), tid("sess"), "u", "status_update", []byte(`{}`))
				done <- err
			}()

			waitFor(t, func() bool { return repo.calls.Load() >= 2 })
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, ErrHeldForRedelivery) {
					t.Fatalf("err = %v, want ErrHeldForRedelivery", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Append did not return after the gateway stopped")
			}
			d.Release()
			if acks.Load() != 0 {
				t.Fatal("a held event was acknowledged")
			}
		})
	}
}

func TestComplete_LateEventWhileTheMarkWaitsIsDropped(t *testing.T) {
	el := newTestPersistentEventLog(t)
	wrap := &failFromBatchRepo{sseEventStore: el.repo}
	wrap.failFrom.Store(1)
	el.repo = wrap
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("late-while-pending")
	var acks atomic.Int32

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()
	el.Forget(taskID)

	ctx, d = received("m-late", false, &acks)
	defer d.Release()
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); !errors.Is(err, ErrTaskCompleted) {
		t.Fatalf("late event: err = %v, want ErrTaskCompleted", err)
	}
}

type flakyMarkRepo struct {
	sseEventStore
	fails      atomic.Int32
	marks      atomic.Int32
	acks       *atomic.Int32
	acksAtMark atomic.Int32
}

func (r *flakyMarkRepo) MarkCompleted(ctx context.Context, taskID string) error {
	if r.marks.Add(1) == 1 {
		r.acksAtMark.Store(r.acks.Load())
	}
	if r.fails.Add(-1) >= 0 {
		return errors.New("simulated mark failure")
	}
	return r.sseEventStore.MarkCompleted(ctx, taskID)
}

func (r *flakyMarkRepo) MarkCompletedBatch(ctx context.Context, taskIDs []string) error {
	if r.marks.Add(1) == 1 {
		r.acksAtMark.Store(r.acks.Load())
	}
	if r.fails.Add(-1) >= 0 {
		return errors.New("simulated mark failure")
	}
	return r.sseEventStore.MarkCompletedBatch(ctx, taskIDs)
}

func TestComplete_DeferredMarkPrecedesTheAckAndIsRetried(t *testing.T) {
	el := newTestPersistentEventLog(t)
	batch := &failFromBatchRepo{sseEventStore: el.repo}
	batch.failFrom.Store(1)
	var acks atomic.Int32
	repo := &flakyMarkRepo{sseEventStore: batch, acks: &acks}
	repo.fails.Store(1)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("deferred-mark")

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()

	batch.failFrom.Store(0)
	waitFor(t, func() bool {
		done, err := el.repo.IsCompleted(t.Context(), taskID)
		return err == nil && done
	})
	if n := repo.marks.Load(); n < 2 {
		t.Fatalf("marks = %d, want the failed one and a retry", n)
	}
	if n := repo.acksAtMark.Load(); n != 0 {
		t.Fatalf("%d acknowledgements released before the first mark, want 0", n)
	}
	waitFor(t, func() bool { return acks.Load() == 1 })
}

type emptyFirstReadRepo struct {
	sseEventStore
	reads atomic.Int32
}

func (r *emptyFirstReadRepo) GetByTask(ctx context.Context, taskID string, from int) ([]*store.SSEEventBufferRow, error) {
	if r.reads.Add(1) == 1 {
		return nil, nil
	}
	return r.sseEventStore.GetByTask(ctx, taskID, from)
}

func TestSubscribe_ReadsAgainBeforeClosingAnEndedTask(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("subscribe-reread")
	appendStatus(t, first, taskID, 2)
	second.repo = &emptyFirstReadRepo{sseEventStore: second.repo}
	second.SetTaskRunningCheck(func(string) (running, known bool) { return false, true })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ch, err := second.Subscribe(ctx, taskID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for want := int64(1); want <= 2; want++ {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed before event %d", want)
			}
			if evt.Sequence != want {
				t.Fatalf("event %d, want %d", evt.Sequence, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("event %d never arrived", want)
		}
	}
}

func TestComplete_FailedMarkAfterTheFinalRowIsRetried(t *testing.T) {
	el := newTestPersistentEventLog(t)
	var acks atomic.Int32
	repo := &flakyMarkRepo{sseEventStore: el.repo, acks: &acks}
	repo.fails.Store(1)
	el.repo = repo
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("direct-mark")

	ctx, d := received("m-final", false, &acks)
	if _, err := el.Append(ctx, taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	d.Release()

	waitFor(t, func() bool {
		done, err := el.repo.IsCompleted(t.Context(), taskID)
		return err == nil && done
	})
	if n := repo.marks.Load(); n < 2 {
		t.Fatalf("marks = %d, want the failed one and a retry", n)
	}
}
