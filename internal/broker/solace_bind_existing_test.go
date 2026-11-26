// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/config"
	"solace.dev/go/messaging/pkg/solace/message"
	"solace.dev/go/messaging/pkg/solace/resource"
	"solace.dev/go/messaging/pkg/solace/subcode"
)

type existingBindService struct {
	solace.MessagingService
	builder *existingBindBuilder
}

func (f *existingBindService) CreatePersistentMessageReceiverBuilder() solace.PersistentMessageReceiverBuilder {
	return f.builder
}

type existingBindBuilder struct {
	solace.PersistentMessageReceiverBuilder
	strategy      config.MissingResourcesCreationStrategy
	subscriptions int
	outcomes      []config.MessageSettlementOutcome
	queue         *resource.Queue
	receiver      *existingBindReceiver
}

func (f *existingBindBuilder) WithMissingResourcesCreationStrategy(s config.MissingResourcesCreationStrategy) solace.PersistentMessageReceiverBuilder {
	f.strategy = s
	return f
}

func (f *existingBindBuilder) WithSubscriptions(subs ...resource.Subscription) solace.PersistentMessageReceiverBuilder {
	f.subscriptions += len(subs)
	return f
}

func (f *existingBindBuilder) WithRequiredMessageOutcomeSupport(outcomes ...config.MessageSettlementOutcome) solace.PersistentMessageReceiverBuilder {
	f.outcomes = append(f.outcomes, outcomes...)
	return f
}

func (f *existingBindBuilder) Build(q *resource.Queue) (solace.PersistentMessageReceiver, error) {
	f.queue = q
	return f.receiver, nil
}

type existingBindReceiver struct {
	solace.PersistentMessageReceiver
	startErr  error
	removeErr error
	removed   []string
	grace     time.Duration
	closed    bool
	received  []receiveResult
	waits     []time.Duration
	acked     []message.InboundMessage
	settled   []settlement
}

type receiveResult struct {
	in  message.InboundMessage
	err error
}

type settlement struct {
	in      message.InboundMessage
	outcome config.MessageSettlementOutcome
}

func (f *existingBindReceiver) ReceiveMessage(timeout time.Duration) (message.InboundMessage, error) {
	f.waits = append(f.waits, timeout)
	if len(f.received) == 0 {
		time.Sleep(timeout)
		return nil, solace.NewError(&solace.TimeoutError{}, "timed out", nil)
	}
	r := f.received[0]
	f.received = f.received[1:]
	return r.in, r.err
}

func (f *existingBindReceiver) Ack(in message.InboundMessage) error {
	f.acked = append(f.acked, in)
	return nil
}

func (f *existingBindReceiver) Settle(in message.InboundMessage, outcome config.MessageSettlementOutcome) error {
	f.settled = append(f.settled, settlement{in: in, outcome: outcome})
	return nil
}

func (f *existingBindReceiver) Start() error { return f.startErr }

func (f *existingBindReceiver) RemoveSubscription(s resource.Subscription) error {
	f.removed = append(f.removed, s.GetName())
	return f.removeErr
}

func (f *existingBindReceiver) Terminate(grace time.Duration) error {
	f.grace, f.closed = grace, true
	return nil
}

func newExistingBindBroker(t *testing.T, rx *existingBindReceiver) (*SolaceBroker, *existingBindBuilder) {
	t.Helper()
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	builder := &existingBindBuilder{receiver: rx}
	b.svc = &existingBindService{builder: builder}
	return b, builder
}

func TestSolaceBindExisting_NeverCreatesOrRebinds(t *testing.T) {
	t.Parallel()
	rx := &existingBindReceiver{}
	b, builder := newExistingBindBroker(t, rx)

	q, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}
	if builder.strategy != config.PersistentReceiverDoNotCreateMissingResources {
		t.Errorf("strategy = %q, want %q", builder.strategy, config.PersistentReceiverDoNotCreateMissingResources)
	}
	if builder.subscriptions != 0 {
		t.Errorf("bind added %d subscriptions, want 0", builder.subscriptions)
	}
	if builder.queue == nil || builder.queue.GetName() != "ns/q/old" {
		t.Errorf("bound queue = %v, want ns/q/old", builder.queue)
	}
	if _, registered := b.receivers["ns/q/old"]; registered {
		t.Error("BindExisting registered the receiver in b.receivers; the rebind path could recreate the queue")
	}

	if err := q.RemoveSubscription("ns/configuration/model/>"); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}
	if len(rx.removed) != 1 || rx.removed[0] != "ns/configuration/model/>" {
		t.Errorf("removed = %v, want [ns/configuration/model/>]", rx.removed)
	}
	q.Close()
	if !rx.closed || rx.grace != 0 {
		t.Errorf("Close: terminated=%v grace=%v, want terminated with grace 0", rx.closed, rx.grace)
	}
}

func TestSolaceBindExisting_MapsSubcodes(t *testing.T) {
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
			b, _ := newExistingBindBroker(t, &existingBindReceiver{startErr: solace.NewNativeError("refused", tc.code)})
			_, err := b.BindExisting("ns/q/old")
			if !errors.Is(err, tc.want) {
				t.Fatalf("BindExisting err = %v, want it to match %v", err, tc.want)
			}
		})
	}
}

func TestSolaceBoundQueue_RemoveSubscriptionMapsPermission(t *testing.T) {
	t.Parallel()
	b, _ := newExistingBindBroker(t, &existingBindReceiver{
		removeErr: solace.NewNativeError("Permission Not Allowed", subcode.PermissionNotAllowed),
	})
	q, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}
	if err := q.RemoveSubscription("ns/configuration/model/>"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("RemoveSubscription err = %v, want it to match ErrPermissionDenied", err)
	}
}

func TestSolaceBindExisting_NotConnected(t *testing.T) {
	t.Parallel()
	b := NewSolaceBroker(SolaceBrokerConfig{}, testLogger())
	if _, err := b.BindExisting("ns/q/old"); !errors.Is(err, errNotConnected) {
		t.Fatalf("BindExisting err = %v, want errNotConnected", err)
	}
}

func TestSolaceErr(t *testing.T) {
	t.Parallel()
	plain := errors.New("plain")
	other := solace.NewNativeError("timeout", subcode.Timeout)
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"not an SDK error", plain, nil},
		{"other subcode", other, nil},
		{"unknown queue", solace.NewNativeError("Unknown Queue", subcode.UnknownQueueName), ErrQueueNotFound},
		{"permission", solace.NewNativeError("Permission Not Allowed", subcode.PermissionNotAllowed), ErrPermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := solaceErr(tc.in)
			if tc.want == nil {
				if got != tc.in { //nolint:errorlint // identity is the property under test
					t.Fatalf("solaceErr(%v) = %v, want the input unchanged", tc.in, got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("solaceErr(%v) = %v, want it to match %v", tc.in, got, tc.want)
			}
			if !errors.Is(got, tc.in) {
				t.Fatalf("solaceErr(%v) = %v, want the SDK error still wrapped", tc.in, got)
			}
		})
	}
}

func TestSolaceBoundQueue_ReceiveSettlesOnTheBind(t *testing.T) {
	t.Parallel()
	in := fakeDelivered{redelivered: true}
	rx := &existingBindReceiver{received: []receiveResult{
		{err: solace.NewError(&solace.TimeoutError{}, "timed out", nil)},
		{in: in},
	}}
	b, builder := newExistingBindBroker(t, rx)
	q, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}
	if want := []config.MessageSettlementOutcome{config.PersistentReceiverAcceptedOutcome, config.PersistentReceiverFailedOutcome}; !slices.Equal(builder.outcomes, want) {
		t.Errorf("outcome support = %v, want %v", builder.outcomes, want)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := q.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if !msg.Redelivered || msg.Topic != "ns/a2a/v1/status" {
		t.Errorf("message = redelivered %v on %q, want the inbound message's flag and topic", msg.Redelivered, msg.Topic)
	}
	if len(rx.waits) != 2 {
		t.Errorf("ReceiveMessage called %d times, want 2: a timeout keeps waiting", len(rx.waits))
	}

	msg.Ack()
	if len(rx.acked) != 1 || rx.acked[0] != in {
		t.Errorf("acked = %v, want the inbound message once", rx.acked)
	}
	msg.Nack()
	if len(rx.settled) != 1 || rx.settled[0].in != in || rx.settled[0].outcome != config.PersistentReceiverFailedOutcome {
		t.Errorf("settled = %v, want the inbound message once with the failed outcome", rx.settled)
	}
}

func TestSolaceBoundQueue_ReceiveStopsAtTheContextDeadline(t *testing.T) {
	t.Parallel()
	rx := &existingBindReceiver{}
	b, _ := newExistingBindBroker(t, rx)
	q, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}

	const deadline = 30 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()
	if _, err := q.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive err = %v, want DeadlineExceeded", err)
	}
	if len(rx.waits) == 0 {
		t.Fatal("ReceiveMessage was never called")
	}
	for _, w := range rx.waits {
		if w > deadline {
			t.Errorf("ReceiveMessage waited %v, longer than the %v left on the context", w, deadline)
		}
	}
}

func TestSolaceBoundQueue_ReceiveReturnsReceiverErrors(t *testing.T) {
	t.Parallel()
	rx := &existingBindReceiver{received: []receiveResult{
		{err: solace.NewError(&solace.IllegalStateError{}, "receiver terminated", nil)},
	}}
	b, _ := newExistingBindBroker(t, rx)
	q, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}

	_, err = q.Receive(t.Context())
	var state *solace.IllegalStateError
	if !errors.As(err, &state) {
		t.Fatalf("Receive err = %v, want the receiver's IllegalStateError", err)
	}
}

func TestSolaceBoundQueue_ReceiveNeverWaitsForever(t *testing.T) {
	t.Parallel()
	rx := &existingBindReceiver{}
	b, _ := newExistingBindBroker(t, rx)
	q, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}

	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := q.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive err = %v, want DeadlineExceeded", err)
	}
	for _, w := range rx.waits {
		if w < 0 {
			t.Fatalf("ReceiveMessage was called with %v; a negative timeout waits forever", w)
		}
	}
}
