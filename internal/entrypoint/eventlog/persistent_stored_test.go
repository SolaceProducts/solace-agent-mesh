// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type storedRecorder struct {
	mu      sync.Mutex
	el      *PersistentEventLog
	seqs    []int64
	inDB    []bool
	payload map[int64]string
}

func recordStored(t *testing.T, el *PersistentEventLog) *storedRecorder {
	t.Helper()
	r := &storedRecorder{el: el, payload: map[int64]string{}}
	el.SetOnStored(func(taskID string, rows []StoredRow) {
		for _, row := range rows {
			stored, err := el.repo.GetByTask(t.Context(), taskID, int(row.Sequence))
			r.mu.Lock()
			r.seqs = append(r.seqs, row.Sequence)
			r.inDB = append(r.inDB, err == nil && len(stored) > 0 && stored[0].EventSequence == int(row.Sequence))
			r.payload[row.Sequence] = row.EventType
			r.mu.Unlock()
		}
	})
	return r
}

func (r *storedRecorder) sorted() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]int64(nil), r.seqs...)
	slices.Sort(out)
	return out
}

func (r *storedRecorder) allInDB() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ok := range r.inDB {
		if !ok {
			return false
		}
	}
	return true
}

func wantSeqs(t *testing.T, got []int64, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("stored rows announced %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stored rows announced %v, want %v", got, want)
		}
	}
}

func TestOnStored_AfterTheBackgroundFlush(t *testing.T) {
	el := newTestPersistentEventLog(t)
	rec := recordStored(t, el)
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("stored-flush")

	appendStatus(t, el, taskID, 3)
	waitFor(t, func() bool { return len(rec.sorted()) == 3 })
	wantSeqs(t, rec.sorted(), 1, 2, 3)
	if !rec.allInDB() {
		t.Fatal("a row was announced before it was in the database")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if got := rec.payload[2]; got != "status_update" {
		t.Fatalf("announced row 2 has type %q, want status_update", got)
	}
}

func TestOnStored_SynchronousAppend(t *testing.T) {
	el := newTestPersistentEventLog(t)
	rec := recordStored(t, el)
	taskID := tid("stored-sync")

	appendStatus(t, el, taskID, 2)

	wantSeqs(t, rec.sorted(), 1, 2)
	if !rec.allInDB() {
		t.Fatal("a row was announced before it was in the database")
	}
}

func TestOnStored_FlushOfACompletedTask(t *testing.T) {
	el := newTestPersistentEventLog(t)
	rec := recordStored(t, el)
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("stored-complete")

	appendStatus(t, el, taskID, 3)
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { return len(rec.sorted()) == 3 })
	wantSeqs(t, rec.sorted(), 1, 2, 3)
	if !rec.allInDB() {
		t.Fatal("a row was announced before it was in the database")
	}
}

func TestOnStored_SkipsARejectedRow(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.repo = rejectingBatchRepo{sseEventStore: el.repo, badSeq: 2}
	rec := recordStored(t, el)
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	taskID := tid("stored-rejected")

	var acks atomic.Int32
	for _, id := range []string{"m1", "m2", "m3"} {
		ctx, d := received(id, false, &acks)
		if _, err := el.Append(ctx, taskID, tid("sess"), "u", "status_update", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		d.Release()
	}
	waitFor(t, func() bool { return acks.Load() == 3 })
	waitFor(t, func() bool { return len(rec.sorted()) == 2 })
	wantSeqs(t, rec.sorted(), 1, 3)
}

func TestWake_ReadsRowsAnotherProcessWrote(t *testing.T) {
	writer, reader := twoLogsOnOneDB(t)
	reader.pollInterval = time.Hour
	taskID := tid("wake")
	appendStatus(t, writer, taskID, 1)

	ch, err := reader.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	recvEvent(t, ch, 1)

	appendStatus(t, writer, taskID, 1)
	select {
	case got := <-ch:
		t.Fatalf("event %d arrived with no wake and a one hour poll", got.Sequence)
	case <-time.After(200 * time.Millisecond):
	}
	reader.Wake(taskID)
	select {
	case got := <-ch:
		if got.Sequence != 2 {
			t.Fatalf("event after the wake = %d, want 2", got.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wake did not make the subscriber read")
	}
}

func TestSubscribe_ReadsTheTailBeforeTheFlush(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(50, time.Hour, 1000)
	t.Cleanup(el.Stop)
	el.pollInterval = time.Hour
	taskID := tid("tail-subscriber")
	appendDelivered(t, el, taskID, 1)
	waitFor(t, func() bool { return len(dbSeqs(t, el, taskID)) == 1 })

	ch, err := el.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	recvEvent(t, ch, 1)
	appendDelivered(t, el, taskID, 1)
	select {
	case got := <-ch:
		if got.Sequence != 2 {
			t.Fatalf("event = %d, want 2", got.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the appended event did not reach the subscriber from the tail")
	}
	if got := dbSeqs(t, el, taskID); len(got) != 1 {
		t.Fatalf("stored %v: an open subscriber forced the flush", got)
	}
}

func TestSubscribe_ClosesOnACompletionAnotherProcessMarked(t *testing.T) {
	former, writer := twoLogsOnOneDB(t)
	taskID := tid("former-writer")
	appendStatus(t, former, taskID, 2)
	appendStatus(t, writer, taskID, 2)
	if err := writer.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}

	ch, err := former.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e, open := <-ch:
			if !open {
				if len(got) != 4 || got[3] != 4 {
					t.Fatalf("delivered %v, want 1 to 4", got)
				}
				return
			}
			got = append(got, e.Sequence)
		case <-timeout:
			t.Fatalf("the subscriber never closed after delivering %v", got)
		}
	}
}

func TestOnStored_FlushAnnouncesOnlyWrittenRows(t *testing.T) {
	el := newTestPersistentEventLog(t)
	wrap := &failFromBatchRepo{sseEventStore: el.repo}
	wrap.failFrom.Store(2)
	el.repo = wrap
	el.async = true
	el.tail = map[string][]pendingEvent{}
	el.flushBatchSize = 1
	rec := recordStored(t, el)
	taskID, sess := tid("stored-partial"), tid("sess")
	for seq := 1; seq <= 3; seq++ {
		el.tail[taskID] = append(el.tail[taskID], pendingEvent{seq: seq, sessionID: sess, userID: "u", eventType: "status_update", payload: []byte(`{}`), createdAt: int64(seq)})
	}
	el.unflushed = 3
	el.sequences[taskID] = 3

	el.flushOnce(t.Context(), false)

	wantSeqs(t, rec.sorted(), 1)
}

func TestCompleted(t *testing.T) {
	first, second := twoLogsOnOneDB(t)
	taskID := tid("completed")
	appendStatus(t, first, taskID, 1)
	if done, err := second.Completed(t.Context(), taskID); err != nil || done {
		t.Fatalf("Completed before the mark = %v, %v", done, err)
	}
	if err := first.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	for name, el := range map[string]*PersistentEventLog{"writer": first, "other process": second} {
		if done, err := el.Completed(t.Context(), taskID); err != nil || !done {
			t.Fatalf("%s: Completed = %v, %v", name, done, err)
		}
	}

	mem := NewMemoryEventLog()
	if done, _ := mem.Completed(t.Context(), taskID); done {
		t.Fatal("memory log: an unknown task is completed")
	}
	if _, err := mem.Append(t.Context(), taskID, "", "", "status_update", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := mem.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if done, _ := mem.Completed(t.Context(), taskID); !done {
		t.Fatal("memory log: a completed task is not completed")
	}
}
