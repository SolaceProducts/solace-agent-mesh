// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/config"
	"solace.dev/go/messaging/pkg/solace/resource"
	"solace.dev/go/messaging/pkg/solace/subcode"
)

type consumeOnlyService struct {
	solace.MessagingService
	builder     *consumeOnlyBuilder
	provisioner *recordingProvisioner
}

func (f *consumeOnlyService) CreatePersistentMessageReceiverBuilder() solace.PersistentMessageReceiverBuilder {
	return f.builder
}

func (f *consumeOnlyService) EndpointProvisioner() solace.EndpointProvisioner {
	return f.provisioner
}

func (f *consumeOnlyService) Disconnect() error { return nil }

type consumeOnlyBuilder struct {
	solace.PersistentMessageReceiverBuilder
	strategy      config.MissingResourcesCreationStrategy
	subscriptions int
	queue         *resource.Queue
	receiver      *consumeOnlyReceiver
}

func (f *consumeOnlyBuilder) WithMissingResourcesCreationStrategy(s config.MissingResourcesCreationStrategy) solace.PersistentMessageReceiverBuilder {
	f.strategy = s
	return f
}

func (f *consumeOnlyBuilder) WithSubscriptions(subs ...resource.Subscription) solace.PersistentMessageReceiverBuilder {
	f.subscriptions += len(subs)
	return f
}

func (f *consumeOnlyBuilder) WithRequiredMessageOutcomeSupport(...config.MessageSettlementOutcome) solace.PersistentMessageReceiverBuilder {
	return f
}

func (f *consumeOnlyBuilder) Build(q *resource.Queue) (solace.PersistentMessageReceiver, error) {
	f.queue = q
	return f.receiver, nil
}

type consumeOnlyReceiver struct {
	solace.PersistentMessageReceiver
	startErr error
	handler  solace.MessageHandler
	listener solace.TerminationNotificationListener
}

func (f *consumeOnlyReceiver) Start() error { return f.startErr }

func (f *consumeOnlyReceiver) ReceiveAsync(h solace.MessageHandler) error {
	f.handler = h
	return nil
}

func (f *consumeOnlyReceiver) SetTerminationNotificationListener(l solace.TerminationNotificationListener) {
	f.listener = l
}

func (f *consumeOnlyReceiver) Terminate(time.Duration) error { return nil }
func (f *consumeOnlyReceiver) IsTerminated() bool            { return false }

type recordingProvisioner struct {
	solace.EndpointProvisioner
	deprovisioned []string
}

func (p *recordingProvisioner) Deprovision(name string, _ bool) error {
	p.deprovisioned = append(p.deprovisioned, name)
	return nil
}

func newConsumeOnlyBroker(t *testing.T, rx *consumeOnlyReceiver) (*SolaceBroker, *consumeOnlyService) {
	t.Helper()
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	svc := &consumeOnlyService{
		builder:     &consumeOnlyBuilder{receiver: rx},
		provisioner: &recordingProvisioner{},
	}
	b.svc = svc
	return b, svc
}

func newBoundConsumeOnlyBroker(t *testing.T) (*SolaceBroker, *consumeOnlyService, Queue) {
	t.Helper()
	b, svc := newConsumeOnlyBroker(t, &consumeOnlyReceiver{})
	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	return b, svc, q
}

func TestSolaceConsumeExisting_NeverCreatesOrSubscribes(t *testing.T) {
	t.Parallel()
	b, svc := newConsumeOnlyBroker(t, &consumeOnlyReceiver{})

	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	if svc.builder.strategy != config.PersistentReceiverDoNotCreateMissingResources {
		t.Errorf("strategy = %q, want %q", svc.builder.strategy, config.PersistentReceiverDoNotCreateMissingResources)
	}
	if svc.builder.subscriptions != 0 {
		t.Errorf("bind added %d subscriptions, want 0", svc.builder.subscriptions)
	}
	if svc.builder.queue == nil || svc.builder.queue.GetName() != "preprov/q/x" {
		t.Errorf("bound queue = %v, want preprov/q/x", svc.builder.queue)
	}
	if q.Name() != "preprov/q/x" {
		t.Errorf("Queue.Name = %q, want preprov/q/x", q.Name())
	}

	sr, exists := b.receivers["preprov/q/x"]
	if !exists {
		t.Fatal("ConsumeExisting did not register the receiver in b.receivers; Receive cannot find it")
	}
	if !sr.consumeOnly {
		t.Error("receiver.consumeOnly = false, want true")
	}
	if !sr.started {
		t.Error("receiver.started = false, want true (consume-only has no lazy-start path)")
	}
}

func TestSolaceConsumeExisting_ReceiveFindsRegisteredReceiver(t *testing.T) {
	t.Parallel()
	rx := &consumeOnlyReceiver{}
	b, _ := newConsumeOnlyBroker(t, rx)

	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	if rx.handler == nil {
		t.Fatal("ConsumeExisting did not install a ReceiveAsync handler")
	}

	sr := b.receivers["preprov/q/x"]
	sr.msgChan <- &Message{Payload: []byte("hello")}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	msg, err := b.Receive(ctx, q)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Payload) != "hello" {
		t.Fatalf("payload = %q, want %q", msg.Payload, "hello")
	}
}

func TestSolaceConsumeExisting_MapsSubcodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		code subcode.Code
		want error
	}{
		{"missing queue", subcode.UnknownQueueName, ErrQueueNotFound},
		{"not permitted", subcode.PermissionNotAllowed, ErrPermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, _ := newConsumeOnlyBroker(t, &consumeOnlyReceiver{startErr: solace.NewNativeError("refused", tc.code)})
			_, err := b.ConsumeExisting("preprov/q/x")
			if !errors.Is(err, tc.want) {
				t.Fatalf("ConsumeExisting err = %v, want it to match %v", err, tc.want)
			}
			if _, registered := b.receivers["preprov/q/x"]; registered {
				t.Error("a failed bind registered the receiver; a retry would double-register")
			}
		})
	}
}

func TestSolaceConsumeExisting_NotConnected(t *testing.T) {
	t.Parallel()
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	if _, err := b.ConsumeExisting("preprov/q/x"); !errors.Is(err, errNotConnected) {
		t.Fatalf("ConsumeExisting err = %v, want errNotConnected", err)
	}
}

func TestSolaceConsumeExisting_DeprovisionIsNoOp(t *testing.T) {
	t.Parallel()
	b, svc, _ := newBoundConsumeOnlyBroker(t)

	if err := b.DeprovisionQueue("preprov/q/x"); err != nil {
		t.Fatalf("DeprovisionQueue: %v", err)
	}
	if len(svc.provisioner.deprovisioned) != 0 {
		t.Fatalf("Deprovision called for %v; must be a no-op for consume-only queues", svc.provisioner.deprovisioned)
	}
	if _, registered := b.receivers["preprov/q/x"]; registered {
		t.Error("DeprovisionQueue left the local receiver registered")
	}
}

func TestSolaceConsumeExisting_TerminationSignalsBindFailed(t *testing.T) {
	t.Parallel()
	rx := &consumeOnlyReceiver{}
	b, _ := newConsumeOnlyBroker(t, rx)

	if _, err := b.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	if rx.listener == nil {
		t.Fatal("ConsumeExisting did not install a termination listener")
	}

	rx.listener(fakeTerminationEvent{cause: errors.New("session down")})

	sr := b.receivers["preprov/q/x"]
	if sr == nil {
		t.Fatal("termination deregistered the receiver; the lazy rebind path can no longer find it")
	}
	if sr.started {
		t.Error("sr.started = true after termination; needsBind in Receive stays false and rebind never runs")
	}
	if sr.persistRx != nil {
		t.Error("sr.persistRx not cleared after termination; the stale handle would be used by the next bind path")
	}
	select {
	case err := <-sr.bindFailed:
		if !errors.Is(err, errConsumeOnlyTerminated) {
			t.Fatalf("bindFailed err = %v, want errConsumeOnlyTerminated", err)
		}
		if !errors.Is(err, ErrConsumeQueueUnavailable) {
			t.Fatalf("bindFailed err = %v, want it to wrap ErrConsumeQueueUnavailable so RunReceiveLoop takes the Degraded branch", err)
		}
		if errors.Is(err, ErrQueueNotFound) {
			t.Fatalf("bindFailed err = %v wraps ErrQueueNotFound; RunReceiveLoop would exit Fatal on a transient blip", err)
		}
	default:
		t.Fatal("listener did not signal bindFailed")
	}
	select {
	case _, ok := <-sr.msgChan:
		if !ok {
			t.Fatal("termination closed sr.msgChan; the lazy rebind path's Receive would spuriously return ErrQueueNotFound")
		}
	default:
	}
}

func TestSolaceConsumeExisting_ReceiveWakesOnBindFailed(t *testing.T) {
	t.Parallel()
	b, _, q := newBoundConsumeOnlyBroker(t)
	sr := b.receivers["preprov/q/x"]

	want := fmt.Errorf("%w: %w", ErrConsumeQueueUnavailable, errConsumeOnlyTerminated)
	sr.bindFailed <- want

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := b.Receive(ctx, q)
	if !errors.Is(err, errConsumeOnlyTerminated) {
		t.Fatalf("Receive err = %v, want to wake with the signaled errConsumeOnlyTerminated", err)
	}
	if !errors.Is(err, ErrConsumeQueueUnavailable) {
		t.Fatalf("Receive err = %v, want the ErrConsumeQueueUnavailable wrap preserved", err)
	}
}

func TestSolaceConsumeExisting_RebindsAfterTermination(t *testing.T) {
	t.Parallel()
	rx1 := &consumeOnlyReceiver{}
	b, svc := newConsumeOnlyBroker(t, rx1)

	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	rx1.listener(fakeTerminationEvent{cause: errors.New("session down")})

	rx2 := &consumeOnlyReceiver{}
	svc.builder.strategy = ""
	svc.builder.receiver = rx2

	sr := b.receivers["preprov/q/x"]
	sr.msgChan <- &Message{Payload: []byte("after-rebind")}

	rebindCtx, rebindCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer rebindCancel()
	got, err := b.Receive(rebindCtx, q)
	if err != nil {
		t.Fatalf("Receive after rebind err = %v, want success", err)
	}
	if string(got.Payload) != "after-rebind" {
		t.Fatalf("got payload %q, want %q", got.Payload, "after-rebind")
	}
	if sr.persistRx != rx2 {
		t.Error("lazy rebind did not install the fresh receiver on sr.persistRx")
	}
	if svc.builder.strategy != config.PersistentReceiverDoNotCreateMissingResources {
		t.Errorf("rebind strategy = %q, want DoNotCreate (lazy rebind must never create a customer queue)", svc.builder.strategy)
	}
	if rx2.listener == nil {
		t.Error("rebind did not install a termination listener on the fresh receiver")
	}
	if n := len(sr.bindFailed); n != 0 {
		t.Errorf("sr.bindFailed has %d queued errors after successful rebind, want 0 (drain regressed)", n)
	}
}

func TestSolaceConsumeExisting_RebindDrainsBindFailedNoMessage(t *testing.T) {
	t.Parallel()
	rx1 := &consumeOnlyReceiver{}
	b, svc := newConsumeOnlyBroker(t, rx1)

	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	rx1.listener(fakeTerminationEvent{cause: errors.New("session down")})

	rx2 := &consumeOnlyReceiver{}
	svc.builder.receiver = rx2

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = b.Receive(ctx, q)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive after rebind err = %v, want context.DeadlineExceeded; a stale bindFailed error leaked through the drain", err)
	}
}

func TestSolaceConsumeExisting_StaleTerminationIgnored(t *testing.T) {
	t.Parallel()
	rx1 := &consumeOnlyReceiver{}
	b, svc := newConsumeOnlyBroker(t, rx1)

	if _, err := b.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	oldListener := rx1.listener
	if oldListener == nil {
		t.Fatal("initial bind did not install a termination listener")
	}

	rx2 := &consumeOnlyReceiver{}
	svc.builder.receiver = rx2
	b.mu.Lock()
	sr := b.receivers["preprov/q/x"]
	sr.started = false
	sr.persistRx = nil
	sr.provisionFailedUntil = time.Time{}
	if err := b.startPersistentReceiverLocked(sr, "preprov/q/x"); err != nil {
		b.mu.Unlock()
		t.Fatalf("rebind: %v", err)
	}
	b.mu.Unlock()

	if sr.persistRx != rx2 {
		t.Fatalf("rebind did not install the fresh receiver on sr.persistRx")
	}

	oldListener(fakeTerminationEvent{cause: errors.New("stale")})

	if !sr.started {
		t.Error("stale termination cleared sr.started on the fresh receiver")
	}
	if sr.persistRx != rx2 {
		t.Error("stale termination cleared sr.persistRx on the fresh receiver")
	}
	select {
	case err := <-sr.bindFailed:
		t.Fatalf("stale termination signalled bindFailed with %v; a Receive would spuriously wake with it", err)
	default:
	}

	rx2.listener(fakeTerminationEvent{cause: errors.New("fresh down")})
	if sr.started {
		t.Error("fresh termination did not clear sr.started")
	}
	select {
	case err := <-sr.bindFailed:
		if !errors.Is(err, errConsumeOnlyTerminated) {
			t.Fatalf("bindFailed = %v, want errConsumeOnlyTerminated", err)
		}
	default:
		t.Error("fresh termination did not signal bindFailed")
	}
}

func TestSolaceConsumeExisting_QueueMissingOnRetryIsRetryable(t *testing.T) {
	t.Parallel()
	rx := &consumeOnlyReceiver{startErr: solace.NewNativeError("refused", subcode.UnknownQueueName)}
	b, _ := newConsumeOnlyBroker(t, rx)

	sr := &solaceReceiver{
		msgChan:     make(chan *Message, 1),
		bindFailed:  make(chan error, 1),
		named:       true,
		consumeOnly: true,
	}
	b.mu.Lock()
	b.receivers["preprov/q/x"] = sr
	err := b.startPersistentReceiverLocked(sr, "preprov/q/x")
	b.mu.Unlock()

	if err == nil {
		t.Fatal("startPersistentReceiverLocked unexpectedly succeeded with queue-missing")
	}
	if !errors.Is(err, ErrConsumeQueueUnavailable) {
		t.Fatalf("err = %v does not wrap ErrConsumeQueueUnavailable; RunReceiveLoop would miss the retryable-with-own-detail branch and exit Fatal via ErrQueueNotFound", err)
	}
	if !errors.Is(err, ErrQueueNotFound) {
		t.Fatalf("err = %v does not wrap the inner ErrQueueNotFound; the %%w chain was dropped (regression against the whole point of using %%w)", err)
	}
	var native *solace.NativeError
	if !errors.As(err, &native) {
		t.Fatalf("err = %v does not expose the underlying NativeError via errors.As; the chain was flattened to %%s", err)
	}
}

func TestSolaceConsumeExisting_SubscribeRefused(t *testing.T) {
	t.Parallel()
	b, _, q := newBoundConsumeOnlyBroker(t)

	err := b.Subscribe(q, "preprov/evt/>")
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("Subscribe on a consume-only handle must fail with 'consume-only'; got %v", err)
	}
}

func TestSolaceConsumeExisting_UnsubscribeRefused(t *testing.T) {
	t.Parallel()
	b, _, q := newBoundConsumeOnlyBroker(t)

	err := b.Unsubscribe(q, "preprov/evt/>")
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("Unsubscribe on a consume-only handle must fail with 'consume-only'; got %v", err)
	}
}

func TestSolaceConsumeExisting_CreateQueueRefused(t *testing.T) {
	t.Parallel()
	b, _, _ := newBoundConsumeOnlyBroker(t)
	_, err := b.CreateQueue("preprov/q/x", QueueOptions{Named: true})
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("CreateQueue on consume-only name: err = %v, want 'consume-only'", err)
	}
}

func TestSolaceConsumeExisting_ProvisionQueueRefused(t *testing.T) {
	t.Parallel()
	b, _, _ := newBoundConsumeOnlyBroker(t)
	err := b.ProvisionQueue(t.Context(), "preprov/q/x", QueueOptions{Named: true}, "preprov/evt/>")
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("ProvisionQueue on consume-only name: err = %v, want 'consume-only'", err)
	}
}

func TestSolaceConsumeExisting_DeprovisionAfterDeleteStillNoOp(t *testing.T) {
	t.Parallel()
	b, svc, _ := newBoundConsumeOnlyBroker(t)

	if err := b.DeleteQueue("preprov/q/x"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	if _, registered := b.receivers["preprov/q/x"]; registered {
		t.Fatal("DeleteQueue left the receiver registered")
	}
	if err := b.DeprovisionQueue("preprov/q/x"); err != nil {
		t.Fatalf("DeprovisionQueue after DeleteQueue: %v", err)
	}
	if len(svc.provisioner.deprovisioned) != 0 {
		t.Fatalf("Deprovision called for %v after DeleteQueue; must stay a no-op for consume-only queues",
			svc.provisioner.deprovisioned)
	}
}

func TestSolaceConsumeExisting_DeprovisionAfterFailedConsumeStillNoOp(t *testing.T) {
	t.Parallel()
	rx := &consumeOnlyReceiver{startErr: solace.NewNativeError("missing", subcode.UnknownQueueName)}
	b, svc := newConsumeOnlyBroker(t, rx)

	if _, err := b.ConsumeExisting("preprov/q/x"); err == nil {
		t.Fatal("ConsumeExisting unexpectedly succeeded with queue-missing")
	}
	if _, registered := b.receivers["preprov/q/x"]; registered {
		t.Fatal("failed ConsumeExisting registered the receiver")
	}
	if err := b.DeprovisionQueue("preprov/q/x"); err != nil {
		t.Fatalf("DeprovisionQueue after failed ConsumeExisting: %v", err)
	}
	if len(svc.provisioner.deprovisioned) != 0 {
		t.Fatalf("Deprovision called for %v after failed ConsumeExisting; the name must still be sticky-marked",
			svc.provisioner.deprovisioned)
	}
}

func TestSolaceConsumeExisting_RebindPicksConsumeOnlyPath(t *testing.T) {
	t.Parallel()
	b, svc, _ := newBoundConsumeOnlyBroker(t)

	if svc.builder.strategy != config.PersistentReceiverDoNotCreateMissingResources {
		t.Fatalf("first bind strategy = %q, want DoNotCreate", svc.builder.strategy)
	}

	b.mu.Lock()
	sr := b.receivers["preprov/q/x"]
	sr.started = false
	sr.persistRx = nil
	sr.provisionFailedUntil = time.Time{}
	svc.builder.receiver = &consumeOnlyReceiver{}
	svc.builder.strategy = ""
	err := b.startPersistentReceiverLocked(sr, "preprov/q/x")
	b.mu.Unlock()
	if err != nil {
		t.Fatalf("startPersistentReceiverLocked (rebind) err = %v", err)
	}

	if svc.builder.strategy != config.PersistentReceiverDoNotCreateMissingResources {
		t.Fatalf("rebind strategy = %q, want DoNotCreate — dispatch regressed to the create-on-reconnect path", svc.builder.strategy)
	}
	if svc.builder.subscriptions != 0 {
		t.Errorf("rebind added %d subscriptions, want 0", svc.builder.subscriptions)
	}
}

func TestSolaceConsumeExisting_DuplicateBindRejected(t *testing.T) {
	t.Parallel()
	b, _ := newConsumeOnlyBroker(t, &consumeOnlyReceiver{})

	if _, err := b.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("first ConsumeExisting: %v", err)
	}
	_, err := b.ConsumeExisting("preprov/q/x")
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("second ConsumeExisting must fail with 'already bound'; got %v", err)
	}
}

func TestSolaceConsumeExisting_LazyRebindRefusedAfterDelete(t *testing.T) {
	t.Parallel()
	rx1 := &consumeOnlyReceiver{}
	b, svc := newConsumeOnlyBroker(t, rx1)

	if _, err := b.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	b.mu.Lock()
	sr := b.receivers["preprov/q/x"]
	b.mu.Unlock()
	if sr == nil {
		t.Fatal("ConsumeExisting did not register sr")
	}

	rx2 := &consumeOnlyReceiver{}
	svc.builder.receiver = rx2
	b.mu.Lock()
	sr.started = false
	sr.persistRx = nil
	delete(b.receivers, "preprov/q/x")
	b.mu.Unlock()

	err := b.startPersistentReceiver(sr, "preprov/q/x")
	if !errors.Is(err, ErrQueueNotFound) {
		t.Fatalf("startPersistentReceiver err = %v, want ErrQueueNotFound", err)
	}
	if sr.persistRx != nil {
		t.Error("guard was bypassed: a fresh flow was attached to the stale sr")
	}
	if sr.started {
		t.Error("guard was bypassed: stale sr marked started")
	}
}
