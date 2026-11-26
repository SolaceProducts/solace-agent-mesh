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
	"sync/atomic"
	"testing"
	"time"
)

func TestIdler_Run_FetchesOnExistsEvent(t *testing.T) {
	t.Parallel()

	provider := NewFakeProvider([]RawMessage{
		{UID: "1", RFC822: []byte("From: a\r\n\r\nhi")},
	})
	provider.IdleSupported = true
	provider.IdleEvents = make(chan IdleEvent, 4)

	var enqueued atomic.Int32
	queue := NewMessageQueue(QueueConfig{
		Capacity: 8,
		Workers:  1,
		Handler: func(_ context.Context, _ RawMessage) {
			enqueued.Add(1)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	var inFlight inFlightSet
	idler := &Idler{
		Provider:    provider,
		Queue:       queue,
		FetchLimit:  25,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
		Claim:       inFlight.Claim,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() { _ = idler.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && enqueued.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := enqueued.Load(); got != 1 {
		t.Fatalf("after cold start: enqueued = %d, want 1", got)
	}

	provider.mu.Lock()
	provider.pending = append(provider.pending, RawMessage{UID: "2", RFC822: []byte("From: b\r\n\r\nhi2")})
	provider.mu.Unlock()

	provider.IdleEvents <- IdleEvent{Kind: IdleEventExists}

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && enqueued.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := enqueued.Load(); got != 2 {
		t.Fatalf("after EXISTS event: enqueued = %d, want 2", got)
	}

	if got := provider.StopIdleCount.Load(); got != 1 {
		t.Errorf("StopIdle was called %d times after EXISTS, want 1", got)
	}
}

func TestIdler_Ack_DrainedPath_DirectMarksAndOnAcked(t *testing.T) {
	t.Parallel()

	t.Run("ctx alive: MarkProcessed called and OnAcked fires", func(t *testing.T) {
		t.Parallel()
		fp := NewFakeProvider([]RawMessage{{UID: "u1"}})
		fp.IdleSupported = true
		var acked atomic.Int32
		idler := &Idler{
			Provider: fp,
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			OnAcked:  func(_ ProviderUID) { acked.Add(1) },
		}
		idler.drainAndSeal(t.Context())

		idler.Ack(t.Context(), "u1")

		if got := acked.Load(); got != 1 {
			t.Errorf("OnAcked called %d times; want 1", got)
		}
		if !fp.WasProcessed("u1") {
			t.Error("MarkProcessed was not called for u1 on drained path with live ctx")
		}
	})

	t.Run("ctx cancelled: MarkProcessed skipped but OnAcked still fires", func(t *testing.T) {
		t.Parallel()
		fp := NewFakeProvider([]RawMessage{{UID: "u2"}})
		fp.IdleSupported = true
		fp.MarkProcessedBlock = make(chan struct{})
		t.Cleanup(func() { close(fp.MarkProcessedBlock) })

		var acked atomic.Int32
		idler := &Idler{
			Provider: fp,
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			OnAcked:  func(_ ProviderUID) { acked.Add(1) },
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		idler.drainAndSeal(ctx)
		idler.Ack(ctx, "u2")

		if got := acked.Load(); got != 1 {
			t.Errorf("OnAcked called %d times; want 1", got)
		}
		if fp.WasProcessed("u2") {
			t.Error("MarkProcessed should not run when ctx is cancelled on drained path")
		}
	})
}

func TestIdler_DrainAndSeal_CancelledCtxFiresOnAckedForPending(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider(nil)
	fp.IdleSupported = true
	fp.MarkProcessedBlock = make(chan struct{})
	t.Cleanup(func() { close(fp.MarkProcessedBlock) })

	var acked atomic.Int32
	idler := &Idler{
		Provider: fp,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnAcked:  func(_ ProviderUID) { acked.Add(1) },
	}

	idler.Ack(t.Context(), "u1")
	idler.Ack(t.Context(), "u2")
	idler.Ack(t.Context(), "u3")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	idler.drainAndSeal(ctx)

	if got := acked.Load(); got != 3 {
		t.Errorf("OnAcked fired %d times; want 3 (one per pending UID)", got)
	}
	if fp.WasProcessed("u1") || fp.WasProcessed("u2") || fp.WasProcessed("u3") {
		t.Error("MarkProcessed should not have run on the cancelled-ctx drainAndSeal path")
	}
}

func TestIdler_Run_ReconnectsOnSessionDrop(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider(nil)
	fp.IdleSupported = true

	var enqueued atomic.Int32
	queue := NewMessageQueue(QueueConfig{
		Capacity: 8,
		Workers:  1,
		Handler: func(_ context.Context, _ RawMessage) {
			enqueued.Add(1)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	var inFlight inFlightSet
	idler := &Idler{
		Provider:    fp,
		Queue:       queue,
		FetchLimit:  25,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
		Claim:       inFlight.Claim,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = idler.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fp.idleMu.Lock()
		active := fp.idleActive
		fp.idleMu.Unlock()
		if active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	fp.mu.Lock()
	fp.pending = append(fp.pending, RawMessage{UID: "u-after-drop", RFC822: []byte("From: a\r\n\r\nhi")})
	fp.mu.Unlock()

	fp.SimulateSessionDrop()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && enqueued.Load() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := enqueued.Load(); got != 1 {
		t.Fatalf("after session drop + reconnect: enqueued = %d, want 1", got)
	}
}

func TestIdler_Run_RetriesConnectWithBackoff(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider([]RawMessage{{
		UID:    "u1",
		RFC822: []byte("From: a\r\n\r\nhi"),
	}})
	fp.IdleSupported = true
	fp.ConnectErrorRemaining = 2
	fp.ConnectError = errors.New("fake: imap unreachable")

	var enqueued atomic.Int32
	queue := NewMessageQueue(QueueConfig{
		Capacity: 8,
		Workers:  1,
		Handler:  func(_ context.Context, _ RawMessage) { enqueued.Add(1) },
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	var inFlight inFlightSet
	idler := &Idler{
		Provider:    fp,
		Queue:       queue,
		FetchLimit:  25,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackoffBase: time.Millisecond,
		BackoffMax:  5 * time.Millisecond,
		Claim:       inFlight.Claim,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = idler.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && enqueued.Load() < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := enqueued.Load(); got != 1 {
		t.Fatalf("after connect-retry recovery: enqueued = %d, want 1", got)
	}
}

func TestIdler_StoreFailure_DoesNotDuplicateDispatch(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider([]RawMessage{
		{UID: "u1", RFC822: []byte("From: a\r\n\r\nhi")},
	})
	fp.IdleSupported = true
	fp.MarkProcessedErrorRemaining = 1

	var dispatches atomic.Int32
	var inFlight inFlightSet
	var idler *Idler
	queue := NewMessageQueue(QueueConfig{
		Capacity: 4,
		Workers:  1,
		Handler: func(ctx context.Context, m RawMessage) {
			dispatches.Add(1)
			idler.Ack(ctx, m.UID)
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	idler = &Idler{
		Provider:    fp,
		Queue:       queue,
		FetchLimit:  25,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
		Claim:       inFlight.Claim,
		OnAcked:     inFlight.Done,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = idler.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		fp.mu.Lock()
		failedOnce := fp.MarkProcessedErrorRemaining == 0
		fp.mu.Unlock()
		if dispatches.Load() == 1 && failedOnce {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := dispatches.Load(); got != 1 {
		t.Fatalf("after first cycle: dispatches = %d, want 1", got)
	}

	fp.SimulateSessionDrop()

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fp.WasProcessed("u1") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fp.WasProcessed("u1") {
		t.Fatal("post-reconnect drain did not retry STORE successfully")
	}

	time.Sleep(100 * time.Millisecond)
	if got := dispatches.Load(); got != 1 {
		t.Fatalf("duplicate dispatch detected: dispatches = %d, want 1", got)
	}
}

func TestIdler_Run_RefetchHintRediscoversStuckUnseen(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider([]RawMessage{
		{UID: "u1", RFC822: []byte("From: a\r\n\r\nhi")},
	})
	fp.IdleSupported = true

	var inFlight inFlightSet
	var idler *Idler
	var attempts atomic.Int32
	queue := NewMessageQueue(QueueConfig{
		Capacity: 4,
		Workers:  1,
		Handler: func(_ context.Context, m RawMessage) {
			n := attempts.Add(1)
			inFlight.Done(m.UID)
			if n == 1 {
				idler.RequestRefetch()
			}
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	idler = &Idler{
		Provider:    fp,
		Queue:       queue,
		FetchLimit:  25,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackoffBase: time.Millisecond,
		BackoffMax:  10 * time.Millisecond,
		Claim:       inFlight.Claim,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = idler.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && attempts.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("refetch hint did not trigger re-enqueue: attempts = %d, want >= 2", got)
	}

	if got := fp.StopIdleCount.Load(); got < 1 {
		t.Errorf("StopIdleCount = %d after refetch hint; want >= 1", got)
	}
}

func TestIdler_Keepalive_HealthyCycle(t *testing.T) {
	t.Parallel()

	fp := NewFakeProvider(nil)
	fp.IdleSupported = true

	queue := NewMessageQueue(QueueConfig{
		Capacity: 4,
		Workers:  1,
		Handler:  func(_ context.Context, _ RawMessage) {},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	idler := &Idler{
		Provider:          fp,
		Queue:             queue,
		FetchLimit:        25,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		BackoffBase:       time.Millisecond,
		BackoffMax:        5 * time.Millisecond,
		KeepaliveInterval: 25 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = idler.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && fp.StopIdleCount.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fp.StopIdleCount.Load(); got < 2 {
		t.Fatalf("after multiple keepalive intervals: StopIdleCount = %d, want >= 2", got)
	}

	if !fp.IsConnected() {
		t.Error("provider was unexpectedly disconnected on the healthy keepalive path")
	}
}

func TestIdler_CycleIdleForKeepalive(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("success path returns true after stopping an active IDLE", func(t *testing.T) {
		t.Parallel()
		fp := NewFakeProvider(nil)
		fp.IdleSupported = true
		if _, _, err := fp.Idle(t.Context()); err != nil {
			t.Fatalf("seed Idle: %v", err)
		}
		idler := &Idler{Provider: fp, Logger: logger}

		if !idler.cycleIdleForKeepalive(t.Context(), logger) {
			t.Fatal("cycleIdleForKeepalive returned false on healthy StopIdle; want true")
		}
		if got := fp.StopIdleCount.Load(); got != 1 {
			t.Errorf("StopIdleCount = %d, want 1", got)
		}
	})

	t.Run("StopIdle error returns false", func(t *testing.T) {
		t.Parallel()
		fp := NewFakeProvider(nil)
		fp.IdleSupported = true
		fp.StopIdleError = errors.New("imap idle close: use of closed network connection")
		idler := &Idler{Provider: fp, Logger: logger}

		if idler.cycleIdleForKeepalive(t.Context(), logger) {
			t.Fatal("cycleIdleForKeepalive returned true despite StopIdle error; want false (caller should reconnect)")
		}
	})
}

func TestIdler_Run_DowngradesAfterConsecutiveFailures(t *testing.T) {
	t.Parallel()

	idleStartErr := errors.New("server: BAD command IDLE")
	provider := NewFakeProvider(nil)
	provider.IdleSupported = true
	provider.IdleStartError = idleStartErr

	queue := NewMessageQueue(QueueConfig{
		Capacity: 1,
		Workers:  1,
		Handler:  func(_ context.Context, _ RawMessage) {},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	queue.Start(t.Context())
	t.Cleanup(func() { _ = queue.Stop(context.Background()) })

	idler := &Idler{
		Provider:                   provider,
		Queue:                      queue,
		FetchLimit:                 25,
		Logger:                     slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxConsecutiveIdleFailures: 3,
		BackoffBase:                time.Millisecond,
		BackoffMax:                 5 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)

	err := idler.Run(ctx)
	if !errors.Is(err, errIdleDowngrade) {
		t.Fatalf("Run returned %v, want errIdleDowngrade", err)
	}
}
