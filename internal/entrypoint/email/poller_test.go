// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPoller_Ack_OnAckedFiresEvenOnError(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider(nil)
	fp.MarkProcessedError = errors.New("imap: transient io error")

	var acked atomic.Int32
	p := &Poller{
		Provider: fp,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnAcked:  func(_ ProviderUID) { acked.Add(1) },
	}

	p.Ack(t.Context(), "u1")

	if got := acked.Load(); got != 1 {
		t.Errorf("OnAcked called %d times after MarkProcessed error; want 1", got)
	}
	if fp.WasProcessed("u1") {
		t.Error("MarkProcessed should not have recorded u1 as processed when it returned an error")
	}
}

func TestPoller_FetchesAndAcksAllMessages(t *testing.T) {
	t.Parallel()
	msgs := []RawMessage{{UID: "1"}, {UID: "2"}, {UID: "3"}}
	fp := NewFakeProvider(msgs)

	var seen sync.Map
	var enq int64
	q := NewMessageQueue(QueueConfig{
		Capacity: 10,
		Workers:  1,
		Handler: func(_ context.Context, m RawMessage) {
			if _, loaded := seen.LoadOrStore(m.UID, struct{}{}); !loaded {
				atomic.AddInt64(&enq, 1)
			}
			_ = fp.MarkProcessed(context.Background(), m.UID)
		},
		Logger: slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	p := &Poller{
		Provider:        fp,
		Queue:           q,
		IntervalSeconds: 0,
		FetchLimit:      10,
		Logger:          slog.Default(),
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { _ = p.Run(ctx) })

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&enq) == 3 && fp.WasProcessed("1") && fp.WasProcessed("2") && fp.WasProcessed("3") {
			cancel()
			wg.Wait()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("unique enqueued = %d, want 3", atomic.LoadInt64(&enq))
}

func TestPoller_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	fp := NewFakeProvider(nil)
	q := NewMessageQueue(QueueConfig{
		Capacity: 1,
		Workers:  1,
		Handler:  func(_ context.Context, _ RawMessage) {},
		Logger:   slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	p := &Poller{
		Provider:        fp,
		Queue:           q,
		IntervalSeconds: 30,
		FetchLimit:      10,
		Logger:          slog.Default(),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = p.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Poller.Run did not exit on context cancel")
	}
}
