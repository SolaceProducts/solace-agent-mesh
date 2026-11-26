// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"errors"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/config"
	"solace.dev/go/messaging/pkg/solace/subcode"
)

type fakeTerminationEvent struct {
	cause error
}

func (f fakeTerminationEvent) GetTimestamp() time.Time { return time.Now() }
func (f fakeTerminationEvent) GetMessage() string      { return "fake termination" }
func (f fakeTerminationEvent) GetCause() error         { return f.cause }

func TestSolaceDisconnectCancelsRebind(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())

	b.mu.Lock()
	b.rebindCtx, b.rebindCancel = context.WithCancel(context.Background())
	b.mu.Unlock()

	if err := b.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	b.mu.RLock()
	ctx := b.rebindCtx
	b.mu.RUnlock()

	if ctx.Err() == nil {
		t.Error("expected rebindCtx to be cancelled after Disconnect")
	}
}

func TestSolacePublishNotConnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())

	err := b.Publish(context.Background(), "test/topic", &Message{Payload: []byte("x")})
	if !errors.Is(err, errNotConnected) {
		t.Errorf("Publish: got %v, want errNotConnected", err)
	}

	err = b.PublishGuaranteed(context.Background(), "test/topic", &Message{Payload: []byte("x")})
	if !errors.Is(err, errNotConnected) {
		t.Errorf("PublishGuaranteed: got %v, want errNotConnected", err)
	}
}

func TestSolaceSubscribeDirectNotConnected(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())

	q, err := b.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("CreateDirectSubscription: %v", err)
	}

	err = b.Subscribe(q, "some/topic")
	if !errors.Is(err, errNotConnected) {
		t.Errorf("Subscribe on direct queue with nil svc: got %v, want errNotConnected", err)
	}
}

func TestSolaceOnReceiverTermination_Unregistered(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	sr := &solaceReceiver{msgChan: make(chan *Message, 1)}

	b.onReceiverTermination(sr, "ghost-queue", fakeTerminationEvent{})

	b.mu.RLock()
	inFlight := sr.rebindInFlight
	b.mu.RUnlock()

	if inFlight {
		t.Error("expected rebindInFlight to remain false for unregistered queue")
	}
}

func TestSolaceOnReceiverTermination_NilSvc_CancelExits(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	sr := &solaceReceiver{msgChan: make(chan *Message, 1)}

	b.mu.Lock()
	b.receivers["q1"] = sr
	b.rebindCtx, b.rebindCancel = context.WithCancel(context.Background())
	b.mu.Unlock()

	b.onReceiverTermination(sr, "q1", fakeTerminationEvent{})

	b.mu.RLock()
	inFlight := sr.rebindInFlight
	b.mu.RUnlock()
	if !inFlight {
		t.Fatal("expected rebindInFlight=true immediately after onReceiverTermination")
	}

	b.mu.RLock()
	cancel := b.rebindCancel
	b.mu.RUnlock()
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.RLock()
		done := !sr.rebindInFlight
		b.mu.RUnlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rebindInFlight never cleared after context cancel (5 s deadline)")
}

func TestSolaceOnReceiverTermination_DeregisterExits(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	sr := &solaceReceiver{msgChan: make(chan *Message, 1)}

	b.mu.Lock()
	b.receivers["q2"] = sr
	b.rebindCtx, b.rebindCancel = context.WithCancel(context.Background())
	b.mu.Unlock()

	b.onReceiverTermination(sr, "q2", fakeTerminationEvent{})

	time.Sleep(20 * time.Millisecond)
	b.mu.Lock()
	delete(b.receivers, "q2")
	b.mu.Unlock()

	b.mu.RLock()
	cancel := b.rebindCancel
	b.mu.RUnlock()
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.RLock()
		done := !sr.rebindInFlight
		b.mu.RUnlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("rebindInFlight never cleared after deregister (5 s deadline)")
}

func TestSolaceOnReceiverTermination_DuplicateGuard(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	sr := &solaceReceiver{msgChan: make(chan *Message, 1)}

	b.mu.Lock()
	b.receivers["q3"] = sr
	b.rebindCtx, b.rebindCancel = context.WithCancel(context.Background())
	sr.rebindInFlight = true
	b.mu.Unlock()

	b.onReceiverTermination(sr, "q3", fakeTerminationEvent{})

	b.mu.RLock()
	inFlight := sr.rebindInFlight
	b.mu.RUnlock()

	if !inFlight {
		t.Error("expected rebindInFlight to remain true after duplicate event (guard must not clear it)")
	}

	b.mu.RLock()
	cancel := b.rebindCancel
	b.mu.RUnlock()
	cancel()

	b.mu.Lock()
	sr.rebindInFlight = false
	b.mu.Unlock()
}

func TestSolaceReceive_BindFailedWakesParkedReceive(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	sr := &solaceReceiver{
		msgChan:    make(chan *Message, 1),
		bindFailed: make(chan error, 1),
		named:      true,
		started:    true,
	}
	b.mu.Lock()
	b.receivers["stuck-q"] = sr
	b.mu.Unlock()

	q := &SimpleQueue{QName: "stuck-q"}
	result := make(chan error, 1)
	go func() {
		_, err := b.Receive(context.Background(), q)
		result <- err
	}()

	time.Sleep(20 * time.Millisecond)

	wantErr := errors.New("simulated rebind failure: queue capacity exhausted")
	sr.bindFailed <- wantErr

	select {
	case gotErr := <-result:
		if gotErr != wantErr {
			t.Errorf("Receive returned %v, want the exact bindFailed error %v", gotErr, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not return within 2s of bindFailed signal — parked goroutine still stuck")
	}
}

func TestSolaceReceive_BindFailedWakesPausedParkedReceive(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	sr := &solaceReceiver{
		msgChan:    make(chan *Message, 1),
		bindFailed: make(chan error, 1),
		named:      true,
		started:    true,
	}
	sr.paused.Store(true)

	b.mu.Lock()
	b.receivers["paused-q"] = sr
	b.mu.Unlock()

	q := &SimpleQueue{QName: "paused-q"}
	result := make(chan error, 1)
	go func() {
		_, err := b.Receive(context.Background(), q)
		result <- err
	}()

	time.Sleep(20 * time.Millisecond)

	wantErr := errors.New("simulated rebind failure while paused")
	sr.bindFailed <- wantErr

	select {
	case gotErr := <-result:
		if gotErr != wantErr {
			t.Errorf("Receive returned %v, want the exact bindFailed error %v", gotErr, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not return within 2s of bindFailed signal on the paused branch — still stuck")
	}
}

func TestSolaceRebindReceivers_FailedRebindWakesParkedReceive(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = &fakeMessagingService{
		bindErr: solace.NewNativeError("No More Non-Durable Queue or Topic Endpoint", subcode.NoMoreNonDurableQueueOrTe),
	}

	sr := &solaceReceiver{
		msgChan:    make(chan *Message, 1),
		bindFailed: make(chan error, 1),
		named:      true,
		started:    true,
		topics:     []string{"some/topic"},
	}
	b.mu.Lock()
	b.receivers["blip-q"] = sr
	b.mu.Unlock()

	q := &SimpleQueue{QName: "blip-q"}
	result := make(chan error, 1)
	go func() {
		_, err := b.Receive(context.Background(), q)
		result <- err
	}()

	time.Sleep(20 * time.Millisecond)

	b.rebindReceivers()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Receive returned nil error after a failed rebind — want the rebind failure")
		}
		if errors.Is(err, ErrQueueNotFound) {
			t.Errorf("a rebind bind failure must not surface as the terminal sentinel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not return within 2s of the failed rebind — " +
			"parked goroutine still stuck (the silent-outage bug this test guards against)")
	}
}

func TestSolaceRebindReceivers_ConsumeOnlyGoesThroughConsumeOnlyPath(t *testing.T) {
	t.Parallel()

	svc := &fakeMessagingService{built: make(chan *fakeReceiverBuilder, 2)}
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = svc

	b.mu.Lock()
	b.consumeOnlyNames["preprov/q/x"] = struct{}{}
	initialRx := &fakePersistentReceiver{}
	sr := &solaceReceiver{
		msgChan:     make(chan *Message, 1),
		bindFailed:  make(chan error, 1),
		named:       true,
		consumeOnly: true,
		started:     true,
		persistRx:   initialRx,
	}
	b.receivers["preprov/q/x"] = sr
	b.mu.Unlock()

	b.rebindReceivers()

	if svc.lastReceiverBuilder == nil {
		t.Fatal("rebindReceivers did not build a receiver for the consume-only entry; a parked Receive would hang after the reconnect")
	}

	if svc.lastReceiverBuilder.strategy != config.PersistentReceiverDoNotCreateMissingResources {
		t.Errorf("rebind strategy = %q, want DoNotCreate — rebind would recreate a deleted customer queue",
			svc.lastReceiverBuilder.strategy)
	}

	if svc.lastReceiverBuilder.subscriptions != 0 {
		t.Errorf("rebind attached %d subscriptions, want 0 — SAM must never install subscriptions on a consume-only queue",
			svc.lastReceiverBuilder.subscriptions)
	}

	b.mu.RLock()
	after := sr.persistRx
	b.mu.RUnlock()
	if after == initialRx {
		t.Error("sr.persistRx was not replaced on rebind; stale handle would never deliver")
	}
	if after != svc.lastReceiverBuilder.receiver {
		t.Errorf("sr.persistRx = %p, want the rebuilt receiver %p", after, svc.lastReceiverBuilder.receiver)
	}

	if !sr.consumeOnly {
		t.Error("sr.consumeOnly cleared by rebind; the Deprovision guard would leak on next shutdown")
	}
}

func TestSolaceRebindReceivers_FailedDirectSubscribeWakesParkedReceive(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = &fakeMessagingService{
		bindErr: solace.NewNativeError("No More Non-Durable Queue or Topic Endpoint", subcode.NoMoreNonDurableQueueOrTe),
	}

	sr := &solaceReceiver{
		msgChan:    make(chan *Message, 1),
		bindFailed: make(chan error, 1),
		named:      false,
		started:    true,
		topics:     []string{"some/topic"},
	}
	b.mu.Lock()
	b.receivers["blip-direct-q"] = sr
	b.mu.Unlock()

	q := &SimpleQueue{QName: "blip-direct-q"}
	result := make(chan error, 1)
	go func() {
		_, err := b.Receive(context.Background(), q)
		result <- err
	}()

	time.Sleep(20 * time.Millisecond)

	b.rebindReceivers()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Receive returned nil error after a failed direct rebind — want the rebind failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not return within 2s of the failed direct rebind — " +
			"parked goroutine still stuck (the direct-receiver bindFailed gap)")
	}
}

func TestSolaceRebindReceivers_FailedDirectSubscribeRecoversOnNextHealthyRebind(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = &fakeMessagingService{
		bindErr: solace.NewNativeError("No More Non-Durable Queue or Topic Endpoint", subcode.NoMoreNonDurableQueueOrTe),
	}

	sr := &solaceReceiver{
		msgChan:    make(chan *Message, 1),
		bindFailed: make(chan error, 1),
		named:      false,
		started:    true,
		topics:     []string{"some/topic"},
	}
	b.mu.Lock()
	b.receivers["blip-direct-q"] = sr
	b.mu.Unlock()

	b.rebindReceivers()

	b.mu.RLock()
	stillStarted := sr.started
	b.mu.RUnlock()
	if stillStarted {
		t.Fatal("receiver should not be started after a failed rebind")
	}

	select {
	case <-sr.bindFailed:
	default:
		t.Fatal("expected the failed rebind to leave a signal on bindFailed")
	}

	b.mu.Lock()
	b.svc = &fakeMessagingService{}
	b.mu.Unlock()

	b.rebindReceivers()

	b.mu.RLock()
	defer b.mu.RUnlock()
	if !sr.started {
		t.Fatal("receiver did not recover on the next healthy rebind — permanently-dead-receiver bug")
	}
	if sr.directRx == nil {
		t.Fatal("expected a rebuilt direct receiver after the healthy rebind")
	}
}

func TestSolaceBroker_RestoreSavedBindStateAfterFailedConnect(t *testing.T) {
	t.Parallel()

	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())

	running := &solaceReceiver{msgChan: make(chan *Message, 1), bindFailed: make(chan error, 1), named: true}
	idle := &solaceReceiver{msgChan: make(chan *Message, 1), bindFailed: make(chan error, 1), named: true}
	b.mu.Lock()
	b.receivers["running-q"] = running
	b.receivers["idle-q"] = idle
	b.mu.Unlock()

	saved := map[string]solaceBindState{
		"running-q": {topics: []string{"a/b"}, named: true, started: true},
		"idle-q":    {topics: []string{"c/d"}, named: true, started: false},
		"gone-q":    {topics: []string{"e/f"}, named: true, started: true},
	}
	wantErr := errors.New("simulated connect failure")

	b.mu.Lock()
	b.restoreSavedBindStateLocked(saved, wantErr)
	b.mu.Unlock()

	if got := running.topics; len(got) != 1 || got[0] != "a/b" {
		t.Errorf("running-q topics = %v, want [a/b]", got)
	}
	if !running.started {
		t.Error("running-q started = false, want true (restored from saved state)")
	}
	select {
	case gotErr := <-running.bindFailed:
		if gotErr != wantErr {
			t.Errorf("running-q bindFailed = %v, want %v", gotErr, wantErr)
		}
	default:
		t.Error("running-q bindFailed was not signalled — a parked Receive() would hang forever")
	}

	if got := idle.topics; len(got) != 1 || got[0] != "c/d" {
		t.Errorf("idle-q topics = %v, want [c/d]", got)
	}
	if idle.started {
		t.Error("idle-q started = true, want false (was never started before the attempt)")
	}
	select {
	case gotErr := <-idle.bindFailed:
		t.Errorf("idle-q bindFailed signalled with %v, want no signal — it was never started", gotErr)
	default:
	}
}
