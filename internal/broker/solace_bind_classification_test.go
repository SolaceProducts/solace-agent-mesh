// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/config"
	"solace.dev/go/messaging/pkg/solace/resource"
	"solace.dev/go/messaging/pkg/solace/subcode"
)

type fakeReceiverBuilder struct {
	solace.PersistentMessageReceiverBuilder
	buildErr      error
	listener      solace.ReceiverStateChangeListener
	receiver      *fakePersistentReceiver
	strategy      config.MissingResourcesCreationStrategy
	subscriptions int
	built         chan *fakeReceiverBuilder
}

func (f *fakeReceiverBuilder) WithActivationPassivationSupport(l solace.ReceiverStateChangeListener) solace.PersistentMessageReceiverBuilder {
	f.listener = l
	return f
}

func (f *fakeReceiverBuilder) WithSubscriptions(subs ...resource.Subscription) solace.PersistentMessageReceiverBuilder {
	f.subscriptions += len(subs)
	return f
}

func (f *fakeReceiverBuilder) WithMissingResourcesCreationStrategy(s config.MissingResourcesCreationStrategy) solace.PersistentMessageReceiverBuilder {
	f.strategy = s
	return f
}

func (f *fakeReceiverBuilder) WithRequiredMessageOutcomeSupport(...config.MessageSettlementOutcome) solace.PersistentMessageReceiverBuilder {
	return f
}

func (f *fakeReceiverBuilder) Build(*resource.Queue) (solace.PersistentMessageReceiver, error) {
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	f.receiver = &fakePersistentReceiver{}
	if f.built != nil {
		f.built <- f
	}
	return f.receiver, nil
}

type fakePersistentReceiver struct {
	solace.PersistentMessageReceiver
	terminated   atomic.Bool
	onTerminated solace.TerminationNotificationListener
}

func (f *fakePersistentReceiver) Start() error                             { return nil }
func (f *fakePersistentReceiver) ReceiveAsync(solace.MessageHandler) error { return nil }
func (f *fakePersistentReceiver) Terminate(time.Duration) error            { return nil }
func (f *fakePersistentReceiver) IsTerminated() bool                       { return f.terminated.Load() }
func (f *fakePersistentReceiver) SetTerminationNotificationListener(l solace.TerminationNotificationListener) {
	f.onTerminated = l
}

func (f *fakePersistentReceiver) terminate(te solace.TerminationEvent) {
	f.terminated.Store(true)
	f.onTerminated(te)
}

type fakeDirectReceiverBuilder struct {
	solace.DirectMessageReceiverBuilder
	buildErr error
}

func (f *fakeDirectReceiverBuilder) WithSubscriptions(...resource.Subscription) solace.DirectMessageReceiverBuilder {
	return f
}

func (f *fakeDirectReceiverBuilder) Build() (solace.DirectMessageReceiver, error) {
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return &fakeDirectReceiver{}, nil
}

type fakeDirectReceiver struct {
	solace.DirectMessageReceiver
}

func (f *fakeDirectReceiver) Start() error                             { return nil }
func (f *fakeDirectReceiver) ReceiveAsync(solace.MessageHandler) error { return nil }
func (f *fakeDirectReceiver) Terminate(time.Duration) error            { return nil }

type fakeMessagingService struct {
	solace.MessagingService
	bindErr             error
	lastReceiverBuilder *fakeReceiverBuilder
	built               chan *fakeReceiverBuilder
}

func (f *fakeMessagingService) CreatePersistentMessageReceiverBuilder() solace.PersistentMessageReceiverBuilder {
	f.lastReceiverBuilder = &fakeReceiverBuilder{buildErr: f.bindErr, built: f.built}
	return f.lastReceiverBuilder
}

func (f *fakeMessagingService) CreateDirectMessageReceiverBuilder() solace.DirectMessageReceiverBuilder {
	return &fakeDirectReceiverBuilder{buildErr: f.bindErr}
}

func (f *fakeMessagingService) Disconnect() error { return nil }

func TestSolaceBrokerBind_ClassifiesCapacityFailure(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = &fakeMessagingService{
		bindErr: solace.NewNativeError("No More Non-Durable Queue or Topic Endpoint", subcode.NoMoreNonDurableQueueOrTe),
	}

	q, err := b.CreateQueue("cap-test-q", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if err = b.Subscribe(q, "some/topic"); err != nil {
		t.Fatalf("Subscribe must not fail on a durable queue: %v", err)
	}

	if _, exists := b.receivers[q.Name()]; !exists {
		t.Fatal("receiver must remain registered — capacity errors are retryable, not terminal")
	}

	_, err = b.Receive(context.Background(), q)
	if !errors.Is(err, ErrProvisionCooldown) {
		t.Errorf("Receive during cooldown = %v, want ErrProvisionCooldown", err)
	}
	if errors.Is(err, ErrQueueNotFound) {
		t.Errorf("a capacity cooldown must never surface as the terminal sentinel: %v", err)
	}

	_, err = b.Receive(context.Background(), q)
	if !errors.Is(err, ErrProvisionCooldown) {
		t.Errorf("Receive still within cooldown = %v, want ErrProvisionCooldown", err)
	}
	if errors.Is(err, ErrQueueNotFound) {
		t.Errorf("a capacity cooldown must never surface as the terminal sentinel: %v", err)
	}
}

func TestSolaceBrokerSubscribeTemporaryQueue_PropagatesBindFailure(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = &fakeMessagingService{
		bindErr: solace.NewNativeError("No More Non-Durable Queue or Topic Endpoint", subcode.NoMoreNonDurableQueueOrTe),
	}

	q, err := b.CreateTemporaryQueue()
	if err != nil {
		t.Fatalf("CreateTemporaryQueue: %v", err)
	}

	if err := b.Subscribe(q, "reply/topic"); err == nil {
		t.Fatal("Subscribe on a temporary queue must return the bind failure so the " +
			"caller can fall back; swallowing it strands a synchronous caller")
	}
}

func TestSolaceBrokerBind_UnknownQueueStaysRetryable(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	b.svc = &fakeMessagingService{
		bindErr: solace.NewNativeError("boom", subcode.UnknownQueueName),
	}

	q, err := b.CreateQueue("unknown-q", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if err = b.Subscribe(q, "some/topic"); err != nil {
		t.Fatalf("Subscribe must not fail on a durable queue: %v", err)
	}
	if _, exists := b.receivers[q.Name()]; !exists {
		t.Fatal("receiver must stay registered — a transient failover answer must not kill consumers")
	}

	_, err = b.Receive(context.Background(), q)
	if errors.Is(err, ErrQueueNotFound) {
		t.Errorf("an unknown-queue bind must not surface as terminal to receive loops: %v", err)
	}
}

func TestSolaceBrokerBind_RegistersBindStateListener(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	svc := &fakeMessagingService{}
	b.svc = svc

	var got []BindState
	q, err := b.CreateQueue("bind-state-q", QueueOptions{
		Named:       true,
		OnBindState: func(s BindState) { got = append(got, s) },
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = b.Subscribe(q, "some/topic"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = b.Receive(ctx, q)

	rb := svc.lastReceiverBuilder
	if rb == nil {
		t.Fatal("bind never created a receiver builder")
	}
	if rb.listener == nil {
		t.Fatal("bind did not register the activation listener")
	}

	rb.listener(solace.ReceiverPassive, solace.ReceiverActive, time.Now())
	rb.listener(solace.ReceiverActive, solace.ReceiverPassive, time.Now())
	if len(got) != 2 || got[0] != BindActive || got[1] != BindStandby {
		t.Fatalf("caller saw %v, want [active standby]", got)
	}
}

func bindWithBindState(t *testing.T, b *SolaceBroker, svc *fakeMessagingService, got *[]BindState) *fakeReceiverBuilder {
	t.Helper()
	q, err := b.CreateQueue("bind-state-q", QueueOptions{
		Named:       true,
		OnBindState: func(s BindState) { *got = append(*got, s) },
	})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err = b.Subscribe(q, "some/topic"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = b.Receive(ctx, q)

	rb := svc.lastReceiverBuilder
	if rb == nil || rb.listener == nil {
		t.Fatal("bind did not register the activation listener")
	}
	return rb
}

func TestSolaceBrokerRebindReceivers_ReportsStandbyBeforeRebind(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	svc := &fakeMessagingService{}
	b.svc = svc

	var got []BindState
	first := bindWithBindState(t, b, svc, &got)
	first.listener(solace.ReceiverPassive, solace.ReceiverActive, time.Now())

	b.rebindReceivers()

	second := svc.lastReceiverBuilder
	if second == first || second.listener == nil {
		t.Fatal("rebind did not build a new receiver with the activation listener")
	}
	if len(got) != 2 || got[1] != BindStandby {
		t.Fatalf("caller saw %v before the rebind, want [active standby]", got)
	}

	second.listener(solace.ReceiverPassive, solace.ReceiverActive, time.Now())
	if len(got) != 3 || got[2] != BindActive {
		t.Fatalf("caller saw %v after the rebind won, want [active standby active]", got)
	}
}

func TestSolaceBrokerUnsolicitedTermination_ReportsStandbyBeforeRebind(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	svc := &fakeMessagingService{built: make(chan *fakeReceiverBuilder, 4)}
	b.svc = svc
	b.rebindCtx, b.rebindCancel = context.WithCancel(t.Context())

	var got []BindState
	first := bindWithBindState(t, b, svc, &got)
	<-svc.built
	first.listener(solace.ReceiverPassive, solace.ReceiverActive, time.Now())

	first.receiver.terminate(fakeTerminationEvent{})
	if len(got) != 2 || got[1] != BindStandby {
		t.Fatalf("caller saw %v when the receiver terminated, want [active standby]", got)
	}

	var second *fakeReceiverBuilder
	select {
	case second = <-svc.built:
	case <-time.After(5 * time.Second):
		t.Fatal("re-bind after unsolicited termination never built a receiver")
	}
	if second == first || second.listener == nil {
		t.Fatal("re-bind did not register the activation listener on the new receiver")
	}

	second.listener(solace.ReceiverPassive, solace.ReceiverActive, time.Now())
	if len(got) != 3 || got[2] != BindActive {
		t.Fatalf("caller saw %v after the re-bind won, want [active standby active]", got)
	}
}
