// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type countingRepo struct {
	sseEventStore
	getByTask, isCompleted, states atomic.Int32
}

func (r *countingRepo) GetByTask(ctx context.Context, taskID string, fromSeq int) ([]*store.SSEEventBufferRow, error) {
	r.getByTask.Add(1)
	return r.sseEventStore.GetByTask(ctx, taskID, fromSeq)
}

func (r *countingRepo) IsCompleted(ctx context.Context, taskID string) (bool, error) {
	r.isCompleted.Add(1)
	return r.sseEventStore.IsCompleted(ctx, taskID)
}

func (r *countingRepo) SequenceStates(ctx context.Context, taskIDs []string) (map[string]store.TaskLogState, error) {
	r.states.Add(1)
	return r.sseEventStore.SequenceStates(ctx, taskIDs)
}

func TestSubscribe_TheWritersStreamsReadOnlyWhatIsNew(t *testing.T) {
	el := newTestPersistentEventLog(t)
	count := &countingRepo{sseEventStore: el.repo}
	el.repo = count
	el.EnableAsyncFlush(1000, time.Hour, 10000)
	t.Cleanup(el.Stop)
	const tasks, rows = 20, 5
	var drained sync.WaitGroup
	for i := range tasks {
		taskID := tid(fmt.Sprintf("writer-streams-%d", i))
		appendDelivered(t, el, taskID, 1)
		ch, err := el.Subscribe(t.Context(), taskID, 1)
		if err != nil {
			t.Fatal(err)
		}
		drained.Go(func() {
			for n := range rows + 1 {
				if evt, open := <-ch; !open || evt.Sequence != int64(n+1) {
					t.Errorf("stream %d: event %d = %+v (open %v)", i, n+1, evt, open)
					return
				}
			}
		})
		appendDelivered(t, el, taskID, rows)
	}
	done := make(chan struct{})
	go func() { drained.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the streams did not get every row")
	}
	if got := count.getByTask.Load(); got > tasks {
		t.Fatalf("%d reads for %d streams of %d rows; want at most one per stream", got, tasks, rows+1)
	}

	reads := count.getByTask.Load()
	el.signalFlush()
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
	time.Sleep(5 * el.pollInterval)
	if n := count.getByTask.Load() - reads; n != 0 {
		t.Fatalf("the flush made the streams read the database %d times", n)
	}

	reads, sweeps := count.getByTask.Load(), count.states.Load()
	time.Sleep(30 * el.pollInterval)
	if idle := count.getByTask.Load() - reads; idle != 0 {
		t.Fatalf("idle streams read the database %d times", idle)
	}
	if n := count.states.Load() - sweeps; n == 0 || n > 35 {
		t.Fatalf("%d sweep queries for %d tasks in about 30 intervals; want one per interval", n, tasks)
	}
	if n := count.isCompleted.Load(); n != 0 {
		t.Fatalf("the writer's streams read the completion marker %d times", n)
	}
}

func TestSweep_PicksUpRowsAndCompletionAnotherProcessWrote(t *testing.T) {
	writer, reader := twoLogsOnOneDB(t)
	taskID := tid("sweep-remote")
	appendStatus(t, writer, taskID, 1)
	ch, err := reader.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	next := func() (Event, bool) {
		select {
		case e, open := <-ch:
			return e, open
		case <-time.After(2 * time.Second):
			t.Fatal("nothing from the subscriber")
			return Event{}, false
		}
	}
	if e, _ := next(); e.Sequence != 1 {
		t.Fatalf("event %d, want 1", e.Sequence)
	}
	appendStatus(t, writer, taskID, 1)
	if e, _ := next(); e.Sequence != 2 {
		t.Fatalf("event %d, want 2 from the sweep", e.Sequence)
	}
	if err := writer.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if _, open := next(); open {
		t.Fatal("the subscriber stayed open after the task completed")
	}
}

type failingReadsRepo struct {
	sseEventStore
	fails   atomic.Int32
	block   bool
	entered chan struct{}
	once    sync.Once
}

func (r *failingReadsRepo) GetByTask(ctx context.Context, taskID string, fromSeq int) ([]*store.SSEEventBufferRow, error) {
	if r.block {
		r.once.Do(func() { close(r.entered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if r.fails.Add(-1) >= 0 {
		return nil, errors.New("connection reset")
	}
	return r.sseEventStore.GetByTask(ctx, taskID, fromSeq)
}

func logTo(el *PersistentEventLog) *bytes.Buffer {
	var buf bytes.Buffer
	el.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &buf
}

func TestSubscribe_RetriesAFailedRead(t *testing.T) {
	el := newTestPersistentEventLog(t)
	taskID := tid("read-retry")
	appendStatus(t, el, taskID, 1)
	repo := &failingReadsRepo{sseEventStore: el.repo}
	repo.fails.Store(2)
	el.repo = repo
	logs := logTo(el)

	ch, err := el.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case e, open := <-ch:
		if !open || e.Sequence != 1 {
			t.Fatalf("got %+v open=%v, want event 1 after the retries", e, open)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the read was not retried")
	}
	if strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "retrying") {
		t.Fatalf("want a Warn about the retry and no Error, got:\n%s", logs.String())
	}
}

func TestSubscribe_ClientLeavingDuringAReadLogsNothing(t *testing.T) {
	el := newTestPersistentEventLog(t)
	taskID := tid("read-cancel")
	appendStatus(t, el, taskID, 1)
	repo := &failingReadsRepo{sseEventStore: el.repo, block: true, entered: make(chan struct{})}
	el.repo = repo
	logs := logTo(el)

	ctx, cancel := context.WithCancel(t.Context())
	ch, err := el.Subscribe(ctx, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-repo.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream never read the database")
	}
	cancel()
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("an event arrived after the client left")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end when the client left")
	}
	if strings.Contains(logs.String(), "level=ERROR") || strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("a client leaving was logged:\n%s", logs.String())
	}
}

func TestSubscribe_FinalRowEndsTheStream(t *testing.T) {
	el := newTestPersistentEventLog(t)
	el.pollInterval = time.Hour
	taskID := tid("final-row")
	appendStatus(t, el, taskID, 1)
	if _, err := el.Append(t.Context(), taskID, tid("sess"), "u", "final_response", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	ch, err := el.Subscribe(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for {
		select {
		case e, open := <-ch:
			if !open {
				if len(got) != 2 || got[1] != "final_response" {
					t.Fatalf("delivered %v, want the status row then the final row", got)
				}
				return
			}
			got = append(got, e.EventType)
		case <-time.After(2 * time.Second):
			t.Fatalf("the stream stayed open after %v", got)
		}
	}
}

func TestRead_TailWithAHoleReadsTheDatabase(t *testing.T) {
	writer, reader := twoLogsOnOneDB(t)
	taskID := tid("tail-hole")
	appendStatus(t, writer, taskID, 7)
	reader.async = true
	reader.tail = map[string][]pendingEvent{}
	reader.tail[taskID] = []pendingEvent{{seq: 5, eventType: "status_update"}, {seq: 8, eventType: "status_update"}}

	if evs, err := reader.read(t.Context(), taskID, 5, false); err != nil || len(evs) != 0 {
		t.Fatalf("read without the database = %v, %v; a tail with a hole must not serve it", evs, err)
	}
	evs, err := reader.read(t.Context(), taskID, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, e := range evs {
		seqs = append(seqs, e.Sequence)
	}
	if len(seqs) != 4 || seqs[0] != 5 || seqs[3] != 8 {
		t.Fatalf("read %v, want 5 to 8", seqs)
	}
}
