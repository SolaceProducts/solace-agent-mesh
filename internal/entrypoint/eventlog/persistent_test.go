// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/google/uuid"
)

func tid(slug string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("eventlog-test:"+slug)).String() //nolint:forbidigo // deterministic test ID
}

func newTestPersistentEventLog(t *testing.T) *PersistentEventLog {
	t.Helper()

	sdb := store.TestDB(t)

	repo := store.NewSSEEventRepo(sdb)
	taskRepo := store.NewTaskRepo(sdb)
	logger := slog.Default()
	el := NewPersistentEventLog(repo, taskRepo, logger)
	el.pollInterval = 10 * time.Millisecond
	return el
}

func newTestPersistentEventLogAsync(t *testing.T) *PersistentEventLog {
	t.Helper()
	el := newTestPersistentEventLog(t)
	el.EnableAsyncFlush(50, 5*time.Millisecond, 1000)
	t.Cleanup(el.Stop)
	return el
}

func TestPersistent_Append_SequentialNumbers(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		seq, err := el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if seq != int64(i) {
			t.Errorf("Append %d: got seq %d, want %d", i, seq, i)
		}
	}
}

func TestPersistent_Append_SkipsEmptyKeys(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	if seq, err := el.Append(ctx, "", tid("sess"), "u", "status_update", []byte(`{}`)); err != nil || seq != 0 {
		t.Fatalf("empty taskID: got (seq=%d, err=%v), want (0, nil)", seq, err)
	}
	if seq, err := el.Append(ctx, tid("t1"), "", "u", "status_update", []byte(`{}`)); err != nil || seq != 0 {
		t.Fatalf("empty sessionID: got (seq=%d, err=%v), want (0, nil)", seq, err)
	}

	seq, err := el.Append(ctx, tid("t1"), tid("sess"), "u", "status_update", []byte(`{"ok":true}`))
	if err != nil {
		t.Fatalf("valid Append: %v", err)
	}
	if seq != 1 {
		t.Errorf("valid Append: got seq %d, want 1", seq)
	}

	rows, err := el.repo.GetByTask(ctx, tid("t1"), 0)
	if err != nil {
		t.Fatalf("GetByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d persisted rows, want 1 (empty-key events must not persist)", len(rows))
	}
}

func TestPersistent_Append_AfterComplete(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))
	_ = el.Complete(ctx, tid("t1"))

	_, err := el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error after Complete")
	}
	if !errors.Is(err, ErrTaskCompleted) {
		t.Errorf("expected ErrTaskCompleted, got %v", err)
	}
}

func TestPersistent_Subscribe_ReplayAll(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	for range 3 {
		_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))

	}
	_ = el.Complete(ctx, tid("t1"))

	ch, err := el.Subscribe(ctx, tid("t1"), 1)
	if err != nil {
		t.Fatal(err)
	}

	events := drainCh(ch, 5, time.Second)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.Sequence != int64(i+1) {
			t.Errorf("event %d: seq=%d, want %d", i, evt.Sequence, i+1)
		}
	}
}

func TestPersistent_Subscribe_ReplayFromMiddle(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	for range 5 {
		_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))

	}
	_ = el.Complete(ctx, tid("t1"))

	ch, err := el.Subscribe(ctx, tid("t1"), 3)
	if err != nil {
		t.Fatal(err)
	}

	events := drainCh(ch, 10, time.Second)
	if len(events) != 3 {
		t.Fatalf("expected 3 events (seq 3,4,5), got %d", len(events))
	}
	if events[0].Sequence != 3 {
		t.Errorf("first event seq=%d, want 3", events[0].Sequence)
	}
}

func TestPersistent_Subscribe_LiveDelivery(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{"pre":true}`))

	ch, err := el.Subscribe(ctx, tid("t1"), 1)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case evt := <-ch:
		if evt.Sequence != 1 {
			t.Errorf("expected seq 1, got %d", evt.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pre-existing event")
	}

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{"live":true}`))

	select {
	case evt := <-ch:
		if evt.Sequence != 2 {
			t.Errorf("expected seq 2, got %d", evt.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestPersistent_Subscribe_LiveThenComplete(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))

	ch, err := el.Subscribe(ctx, tid("t1"), 1)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "final_response", []byte(`{}`))
	_ = el.Complete(ctx, tid("t1"))

	events := drainCh(ch, 5, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 more event, got %d", len(events))
	}

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestPersistent_Subscribe_ContextCancellation(t *testing.T) {
	el := newTestPersistentEventLog(t)
	bgCtx := context.Background()

	_, _ = el.Append(bgCtx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))

	ctx, cancel := context.WithCancel(bgCtx)
	ch, err := el.Subscribe(ctx, tid("t1"), 1)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	cancel()

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel to close after context cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel close after cancel")
	}
}

func TestPersistent_Subscribe_MultipleSubscribers(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))

	ch1, _ := el.Subscribe(ctx, tid("t1"), 1)
	ch2, _ := el.Subscribe(ctx, tid("t1"), 1)

	got1 := drainCh(ch1, 1, time.Second)
	got2 := drainCh(ch2, 1, time.Second)

	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("expected both subscribers to get 1 event, got %d and %d", len(got1), len(got2))
	}

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "final_response", []byte(`{}`))
	_ = el.Complete(ctx, tid("t1"))

	got1 = drainCh(ch1, 5, time.Second)
	got2 = drainCh(ch2, 5, time.Second)

	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("expected both subscribers to get 1 more event, got %d and %d", len(got1), len(got2))
	}
}

func TestPersistent_Subscribe_NonexistentTask(t *testing.T) {
	el := newTestPersistentEventLog(t)
	_, err := el.Subscribe(context.Background(), tid("nope"), 1)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestPersistent_Cleanup(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))
	_ = el.Cleanup(ctx, tid("t1"))

	_, err := el.Subscribe(ctx, tid("t1"), 1)
	if err == nil {
		t.Fatal("expected error after Cleanup")
	}
}

func TestPersistent_Concurrent_AppendAndSubscribe(t *testing.T) {
	el := newTestPersistentEventLog(t)
	ctx := context.Background()

	_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{"init":true}`))

	const numAppends = 50
	const numSubscribers = 3

	var wg sync.WaitGroup

	results := make([]int, numSubscribers)
	for i := range numSubscribers {
		wg.Go(func() {
			ch, err := el.Subscribe(ctx, tid("t1"), 1)
			if err != nil {
				t.Errorf("subscriber %d: %v", i, err)
				return
			}
			count := 0
			for range ch {
				count++
			}
			results[i] = count
		})
	}

	time.Sleep(50 * time.Millisecond)

	for range numAppends {
		_, _ = el.Append(ctx, tid("t1"), tid("sess"), "", "status_update", []byte(`{}`))

	}

	time.Sleep(200 * time.Millisecond)
	_ = el.Complete(ctx, tid("t1"))

	wg.Wait()

	expected := 1 + numAppends
	for i, count := range results {
		if count != expected {
			t.Errorf("subscriber %d: got %d events, want %d", i, count, expected)
		}
	}
}

func drainCh(ch <-chan Event, max int, timeout time.Duration) []Event {
	var events []Event
	timer := time.After(timeout)
	for range max {
		select {
		case evt, open := <-ch:
			if !open {
				return events
			}
			events = append(events, evt)
		case <-timer:
			return events
		}
	}
	return events
}
