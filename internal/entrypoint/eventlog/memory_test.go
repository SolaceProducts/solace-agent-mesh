// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAppend_SequentialNumbers(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()

	for i := 1; i <= 5; i++ {
		seq, err := m.Append(context.Background(), "t1", "", "", "status_update", []byte(`{}`))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		if seq != int64(i) {
			t.Errorf("Append %d: got seq %d, want %d", i, seq, i)
		}
	}
}

func TestAppend_AfterComplete(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	_, _ = m.Append(context.Background(), "t1", "", "", "status_update", []byte(`{}`))
	_ = m.Complete(context.Background(), "t1")

	_, err := m.Append(context.Background(), "t1", "", "", "status_update", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error after Complete")
	}
	if !errors.Is(err, ErrTaskCompleted) {
		t.Errorf("expected ErrTaskCompleted, got %v", err)
	}
}

func TestSubscribe_ReplayAll(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	for range 3 {
		_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
	}
	_ = m.Complete(ctx, "t1")

	ch, err := m.Subscribe(ctx, "t1", 1)
	if err != nil {
		t.Fatal(err)
	}

	events := drain(ch, 3, time.Second)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, evt := range events {
		if evt.Sequence != int64(i+1) {
			t.Errorf("event %d: seq=%d, want %d", i, evt.Sequence, i+1)
		}
	}
}

func TestSubscribe_ReplayFromMiddle(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	for range 5 {
		_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))

	}
	_ = m.Complete(ctx, "t1")

	ch, err := m.Subscribe(ctx, "t1", 3)
	if err != nil {
		t.Fatal(err)
	}

	events := drain(ch, 5, time.Second)
	if len(events) != 3 {
		t.Fatalf("expected 3 events (seq 3,4,5), got %d", len(events))
	}
	if events[0].Sequence != 3 {
		t.Errorf("first event seq=%d, want 3", events[0].Sequence)
	}
	if events[2].Sequence != 5 {
		t.Errorf("last event seq=%d, want 5", events[2].Sequence)
	}
}

func TestSubscribe_LiveDelivery(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{"pre":true}`))

	ch, err := m.Subscribe(ctx, "t1", 1)
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

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{"live":true}`))

	select {
	case evt := <-ch:
		if evt.Sequence != 2 {
			t.Errorf("expected seq 2, got %d", evt.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestSubscribe_LiveThenComplete(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))

	ch, err := m.Subscribe(ctx, "t1", 1)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}

	_, _ = m.Append(ctx, "t1", "", "", "final_response", []byte(`{}`))
	_ = m.Complete(ctx, "t1")

	events := drain(ch, 5, time.Second)
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

func TestSubscribe_ContextCancellation(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	bgCtx := context.Background()

	_, _ = m.Append(bgCtx, "t1", "", "", "status_update", []byte(`{}`))

	ctx, cancel := context.WithCancel(bgCtx)
	ch, err := m.Subscribe(ctx, "t1", 1)
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

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))

	ch1, _ := m.Subscribe(ctx, "t1", 1)
	ch2, _ := m.Subscribe(ctx, "t1", 1)

	got1 := drain(ch1, 1, time.Second)
	got2 := drain(ch2, 1, time.Second)

	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("expected both subscribers to get 1 event, got %d and %d", len(got1), len(got2))
	}

	_, _ = m.Append(ctx, "t1", "", "", "final_response", []byte(`{}`))
	_ = m.Complete(ctx, "t1")

	got1 = drain(ch1, 5, time.Second)
	got2 = drain(ch2, 5, time.Second)

	if len(got1) != 1 || len(got2) != 1 {
		t.Fatalf("expected both subscribers to get 1 more event, got %d and %d", len(got1), len(got2))
	}
}

func TestSubscribe_NonexistentTask(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	_, err := m.Subscribe(context.Background(), "nope", 1)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestComplete_Idempotent(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
	_ = m.Complete(ctx, "t1")
	err := m.Complete(ctx, "t1")
	if err != nil {
		t.Fatalf("second Complete should not error: %v", err)
	}
}

func TestCleanup_RemovesTask(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
	_ = m.Cleanup(ctx, "t1")

	_, err := m.Subscribe(ctx, "t1", 1)
	if err == nil {
		t.Fatal("expected error after Cleanup")
	}
}

func TestConcurrent_AppendAndSubscribe(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{"init":true}`))

	const numAppends = 100
	const numSubscribers = 5

	var wg sync.WaitGroup

	results := make([]int, numSubscribers)
	for i := range numSubscribers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ch, err := m.Subscribe(ctx, "t1", 1)
			if err != nil {
				t.Errorf("subscriber %d: %v", idx, err)
				return
			}
			count := 0
			for range ch {
				count++
			}
			results[idx] = count
		}(i)
	}

	time.Sleep(10 * time.Millisecond)

	for range numAppends {
		wg.Go(func() {
			_, _ = m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
		})
	}

	time.Sleep(100 * time.Millisecond)
	_ = m.Complete(ctx, "t1")

	wg.Wait()

	expected := 1 + numAppends
	for i, count := range results {
		if count != expected {
			t.Errorf("subscriber %d: got %d events, want %d", i, count, expected)
		}
	}
}

func TestAppend_MaxPayloadSize(t *testing.T) {
	t.Parallel()
	m := NewMemoryEventLog()
	ctx := context.Background()

	small := make([]byte, 1024)
	_, err := m.Append(ctx, "t1", "", "", "status_update", small)
	if err != nil {
		t.Fatalf("Append small payload: %v", err)
	}

	big := make([]byte, defaultMaxPayloadBytes+1)
	_, err = m.Append(ctx, "t1", "", "", "status_update", big)
	if err == nil {
		t.Fatal("expected error for oversized payload")
	}
}

func TestAppend_MaxEventsPerTask(t *testing.T) {
	t.Parallel()
	m := &MemoryEventLog{
		tasks:            make(map[string]*taskLog),
		maxEventsPerTask: 5,
		maxPayloadBytes:  defaultMaxPayloadBytes,
	}
	ctx := context.Background()

	for i := range 5 {
		_, err := m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))

		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	_, err := m.Append(ctx, "t1", "", "", "status_update", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error when max events exceeded")
	}
}

func drain(ch <-chan Event, max int, timeout time.Duration) []Event {
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
