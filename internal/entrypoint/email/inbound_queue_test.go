// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestMessageQueue_DispatchesAllMessages(t *testing.T) {
	t.Parallel()
	var processed int64
	q := NewMessageQueue(QueueConfig{
		Capacity: 10,
		Workers:  2,
		Handler: func(_ context.Context, _ RawMessage) {
			atomic.AddInt64(&processed, 1)
		},
		Logger: slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	for i := 0; i < 5; i++ {
		q.Enqueue(t.Context(), RawMessage{UID: ProviderUID(string(rune('a' + i)))})
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&processed) == 5 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("processed = %d, want 5", atomic.LoadInt64(&processed))
}

func TestMessageQueue_BackpressureBlocksProducer(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	q := NewMessageQueue(QueueConfig{
		Capacity: 1,
		Workers:  1,
		Handler: func(_ context.Context, _ RawMessage) {
			<-release
		},
		Logger: slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	q.Enqueue(t.Context(), RawMessage{UID: "1"})
	q.Enqueue(t.Context(), RawMessage{UID: "2"})

	enqueued := make(chan struct{})
	go func() {
		q.Enqueue(t.Context(), RawMessage{UID: "3"})
		close(enqueued)
	}()
	select {
	case <-enqueued:
		t.Fatal("Enqueue did not block when queue full")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		t.Fatal("Enqueue did not unblock after release")
	}
}

func TestMessageQueue_PanicRecovered(t *testing.T) {
	t.Parallel()
	var processed int64
	q := NewMessageQueue(QueueConfig{
		Capacity: 5,
		Workers:  1,
		Handler: func(_ context.Context, m RawMessage) {
			if m.UID == "panic" {
				panic("test panic")
			}
			atomic.AddInt64(&processed, 1)
		},
		Logger: slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	q.Enqueue(t.Context(), RawMessage{UID: "panic"})
	q.Enqueue(t.Context(), RawMessage{UID: "ok"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&processed) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("processed = %d after panic recovery, want 1", atomic.LoadInt64(&processed))
}
