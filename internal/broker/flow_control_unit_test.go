// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
)

type fakeFlowReceiver struct {
	pauseCalls  atomic.Int64
	resumeCalls atomic.Int64
	pauseErr    error
	resumeErr   error
}

func (f *fakeFlowReceiver) Pause() error  { f.pauseCalls.Add(1); return f.pauseErr }
func (f *fakeFlowReceiver) Resume() error { f.resumeCalls.Add(1); return f.resumeErr }

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestBackpressureThresholdInvariants(t *testing.T) {
	if persistentResumeLowWater >= persistentPauseHighWater {
		t.Fatalf("resume low-water (%d) must be below pause high-water (%d) for hysteresis",
			persistentResumeLowWater, persistentPauseHighWater)
	}
	if persistentPauseHighWater*4 > DefaultMessageChannelSize {
		t.Fatalf("pause high-water (%d) too close to channel capacity (%d): no in-flight cushion",
			persistentPauseHighWater, DefaultMessageChannelSize)
	}
	if persistentResumeRecheckInterval <= 0 {
		t.Fatal("resume re-check interval must be positive so a paused+drained receiver resumes without traffic")
	}
}

func TestCreateQueue_RequestReplyAndWorkQueuesNeverPauseUnlessOptedIn(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())

	if _, err := b.CreateQueue("firehose", QueueOptions{Named: true, Backpressure: true}); err != nil {
		t.Fatalf("CreateQueue firehose: %v", err)
	}
	if _, err := b.CreateQueue("rpc", QueueOptions{Named: true}); err != nil {
		t.Fatalf("CreateQueue rpc: %v", err)
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.receivers["firehose"].backpressure {
		t.Error("Backpressure:true queue must enable the pause path")
	}
	if b.receivers["rpc"].backpressure {
		t.Error("default queue must NOT enable the pause path — pausing it would stall its callers")
	}
}

func TestPauseForBackpressure_OncePerEpisode(t *testing.T) {
	rx := &fakeFlowReceiver{}
	var paused atomic.Bool

	if !pauseForBackpressure(rx, &paused, quietLogger(), "q") {
		t.Fatal("first call should report it paused")
	}
	for range 3 {
		if pauseForBackpressure(rx, &paused, quietLogger(), "q") {
			t.Fatal("a call while already paused must not re-pause")
		}
	}

	if got := rx.pauseCalls.Load(); got != 1 {
		t.Errorf("Pause called %d times, want 1 (once per episode)", got)
	}
	if !paused.Load() {
		t.Error("paused flag should be set after pausing")
	}
}

func TestPauseForBackpressure_ErrorClearsFlagSoItRetries(t *testing.T) {
	rx := &fakeFlowReceiver{pauseErr: errors.New("pause boom")}
	var paused atomic.Bool

	if pauseForBackpressure(rx, &paused, quietLogger(), "q") {
		t.Fatal("a failed Pause must not report success")
	}
	if paused.Load() {
		t.Error("a failed Pause must clear the flag so a later callback retries")
	}

	rx.pauseErr = nil
	if !pauseForBackpressure(rx, &paused, quietLogger(), "q") {
		t.Fatal("retry should pause once the error clears")
	}
	if rx.pauseCalls.Load() != 2 || !paused.Load() {
		t.Errorf("expected a retry that pauses; pauseCalls=%d paused=%v", rx.pauseCalls.Load(), paused.Load())
	}
}

func TestPauseForBackpressure_NilReceiverIsNoOp(t *testing.T) {
	var paused atomic.Bool
	if pauseForBackpressure(nil, &paused, quietLogger(), "q") {
		t.Fatal("nil receiver must not report a pause")
	}
	if paused.Load() {
		t.Error("nil receiver must not flip the paused flag")
	}
}

func TestResumeIfDrained_ResumesOnlyWhenPausedAndDrained(t *testing.T) {
	const lowWater = 100

	t.Run("resumes when paused and at/below low-water", func(t *testing.T) {
		rx := &fakeFlowReceiver{}
		var paused atomic.Bool
		paused.Store(true)
		if !resumeIfDrained(rx, &paused, lowWater, lowWater, quietLogger(), "q") {
			t.Fatal("should report it resumed")
		}
		if rx.resumeCalls.Load() != 1 || paused.Load() {
			t.Errorf("expected resume; resumeCalls=%d paused=%v", rx.resumeCalls.Load(), paused.Load())
		}
	})

	t.Run("no-op above low-water", func(t *testing.T) {
		rx := &fakeFlowReceiver{}
		var paused atomic.Bool
		paused.Store(true)
		if resumeIfDrained(rx, &paused, lowWater+1, lowWater, quietLogger(), "q") {
			t.Fatal("must not resume above low-water")
		}
		if rx.resumeCalls.Load() != 0 || !paused.Load() {
			t.Errorf("must stay paused above low-water; resumeCalls=%d paused=%v", rx.resumeCalls.Load(), paused.Load())
		}
	})

	t.Run("no-op when not paused", func(t *testing.T) {
		rx := &fakeFlowReceiver{}
		var paused atomic.Bool
		if resumeIfDrained(rx, &paused, 0, lowWater, quietLogger(), "q") {
			t.Fatal("must not resume a receiver that isn't paused")
		}
		if rx.resumeCalls.Load() != 0 {
			t.Errorf("resumeCalls=%d, want 0", rx.resumeCalls.Load())
		}
	})

	t.Run("nil receiver is a no-op", func(t *testing.T) {
		var paused atomic.Bool
		paused.Store(true)
		if resumeIfDrained(nil, &paused, 0, lowWater, quietLogger(), "q") {
			t.Fatal("nil receiver must not report a resume")
		}
	})

	t.Run("error re-sets the flag and a later drain retries", func(t *testing.T) {
		rx := &fakeFlowReceiver{resumeErr: errors.New("resume boom")}
		var paused atomic.Bool
		paused.Store(true)
		if resumeIfDrained(rx, &paused, 0, lowWater, quietLogger(), "q") {
			t.Fatal("a failed Resume must not report success")
		}
		if !paused.Load() {
			t.Fatal("a failed Resume must keep the paused flag set")
		}
		rx.resumeErr = nil
		if !resumeIfDrained(rx, &paused, 0, lowWater, quietLogger(), "q") {
			t.Fatal("retry should resume once the error clears")
		}
		if rx.resumeCalls.Load() != 2 || paused.Load() {
			t.Errorf("expected retry resume; resumeCalls=%d paused=%v", rx.resumeCalls.Load(), paused.Load())
		}
	})
}

func TestBackpressure_PauseResumeCycle(t *testing.T) {
	rx := &fakeFlowReceiver{}
	var paused atomic.Bool
	const lowWater = 10

	pauseForBackpressure(rx, &paused, quietLogger(), "q")
	if !paused.Load() {
		t.Fatal("should be paused after the channel filled")
	}
	resumeIfDrained(rx, &paused, lowWater+5, lowWater, quietLogger(), "q")
	if !paused.Load() || rx.resumeCalls.Load() != 0 {
		t.Fatal("must stay paused until drained to low-water")
	}
	resumeIfDrained(rx, &paused, lowWater, lowWater, quietLogger(), "q")
	if paused.Load() || rx.resumeCalls.Load() != 1 {
		t.Fatalf("expected resume after draining; paused=%v resumeCalls=%d", paused.Load(), rx.resumeCalls.Load())
	}
}

func TestBackpressure_ConcurrentPauseResumeNoStuckState(t *testing.T) {
	rx := &fakeFlowReceiver{}
	var paused atomic.Bool
	const (
		goroutines = 8
		iterations = 2000
	)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				pauseForBackpressure(rx, &paused, quietLogger(), "q")
			}
		})
		wg.Go(func() {
			for range iterations {
				resumeIfDrained(rx, &paused, 0, 100, quietLogger(), "q")
			}
		})
	}
	wg.Wait()

	pauses, resumes := rx.pauseCalls.Load(), rx.resumeCalls.Load()
	diff := pauses - resumes
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Fatalf("pause/resume imbalance %d (pauses=%d resumes=%d): a transition was lost", diff, pauses, resumes)
	}

	if paused.Load() {
		if !resumeIfDrained(rx, &paused, 0, 100, quietLogger(), "q") {
			t.Fatal("a paused receiver must be resumable — stuck-paused state")
		}
	}
	if paused.Load() {
		t.Fatal("receiver should be unpaused at rest")
	}
}

type fakePersistRx struct {
	solace.PersistentMessageReceiver
	resumeCalls atomic.Int64
}

func (f *fakePersistRx) Resume() error { f.resumeCalls.Add(1); return nil }
func (f *fakePersistRx) Pause() error  { return nil }

func TestEnqueuePersistent_BackpressureGating(t *testing.T) {
	fill := func(sr *solaceReceiver, n int) {
		for range n {
			sr.msgChan <- &Message{}
		}
	}
	newSR := func(backpressure bool) *solaceReceiver {
		return &solaceReceiver{msgChan: make(chan *Message, DefaultMessageChannelSize), named: true, backpressure: backpressure}
	}

	t.Run("firehose pauses at high-water", func(t *testing.T) {
		b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())
		sr := newSR(true)
		fill(sr, persistentPauseHighWater)
		rx := &fakeFlowReceiver{}
		b.enqueuePersistent(sr, &Message{}, rx, "q")
		if rx.pauseCalls.Load() != 1 || !sr.paused.Load() {
			t.Errorf("firehose at high-water must pause; pauseCalls=%d paused=%v", rx.pauseCalls.Load(), sr.paused.Load())
		}
	})

	t.Run("non-firehose never pauses at high-water", func(t *testing.T) {
		b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())
		sr := newSR(false)
		fill(sr, persistentPauseHighWater)
		rx := &fakeFlowReceiver{}
		b.enqueuePersistent(sr, &Message{}, rx, "q")
		if rx.pauseCalls.Load() != 0 || sr.paused.Load() {
			t.Errorf("non-firehose must NOT pause; pauseCalls=%d paused=%v", rx.pauseCalls.Load(), sr.paused.Load())
		}
	})

	t.Run("full channel NACKs to requeue; non-firehose still never pauses", func(t *testing.T) {
		b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())
		sr := newSR(false)
		fill(sr, DefaultMessageChannelSize)
		rx := &fakeFlowReceiver{}
		var nacked bool
		msg := NewDeliveredMessage("id", "t", nil, func() {}, func() { nacked = true })
		b.enqueuePersistent(sr, msg, rx, "q")
		if !nacked {
			t.Error("full channel must NACK to requeue")
		}
		if rx.pauseCalls.Load() != 0 || sr.paused.Load() {
			t.Errorf("non-firehose must not pause even when full; pauseCalls=%d", rx.pauseCalls.Load())
		}
	})

	t.Run("full channel firehose NACKs and pauses", func(t *testing.T) {
		b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())
		sr := newSR(true)
		fill(sr, DefaultMessageChannelSize)
		rx := &fakeFlowReceiver{}
		var nacked bool
		msg := NewDeliveredMessage("id", "t", nil, func() {}, func() { nacked = true })
		b.enqueuePersistent(sr, msg, rx, "q")
		if !nacked || rx.pauseCalls.Load() != 1 || !sr.paused.Load() {
			t.Errorf("firehose full must NACK+pause; nacked=%v pauseCalls=%d paused=%v", nacked, rx.pauseCalls.Load(), sr.paused.Load())
		}
	})
}

func TestReceive_ResumesWithoutTraffic(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())
	rx := &fakePersistRx{}
	sr := &solaceReceiver{
		msgChan:      make(chan *Message, DefaultMessageChannelSize),
		named:        true,
		started:      true,
		backpressure: true,
		persistRx:    rx,
	}
	sr.paused.Store(true)
	b.receivers["q"] = sr

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := b.Receive(ctx, &SimpleQueue{QName: "q"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline (no message was ever delivered), got %v", err)
	}
	if rx.resumeCalls.Load() == 0 {
		t.Error("paused+drained receiver must resume without waiting for a message")
	}
	if sr.paused.Load() {
		t.Error("receiver must be unpaused after draining")
	}
}

func TestReceive_NotPausedReturnsMessage(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, quietLogger())
	sr := &solaceReceiver{msgChan: make(chan *Message, 4), named: true, started: true}
	want := &Message{ID: "m1"}
	sr.msgChan <- want
	b.receivers["q"] = sr

	got, err := b.Receive(context.Background(), &SimpleQueue{QName: "q"})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}
