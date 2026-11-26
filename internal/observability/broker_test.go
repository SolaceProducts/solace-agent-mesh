// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type stubBroker struct {
	publishFn           func(ctx context.Context, topic string, msg *broker.Message) error
	publishGuaranteedFn func(ctx context.Context, topic string, msg *broker.Message) error
}

func (s *stubBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	if s.publishFn != nil {
		return s.publishFn(ctx, topic, msg)
	}
	return nil
}

func (s *stubBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if s.publishGuaranteedFn != nil {
		return s.publishGuaranteedFn(ctx, topic, msg)
	}
	return nil
}

func (s *stubBroker) Connect(context.Context) error    { return nil }
func (s *stubBroker) Disconnect(context.Context) error { return nil }
func (s *stubBroker) IsConnected() bool                { return true }
func (s *stubBroker) CreateQueue(string, broker.QueueOptions) (broker.Queue, error) {
	return nil, nil
}
func (s *stubBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (s *stubBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (s *stubBroker) CreateDirectSubscription() (broker.Queue, error) { return nil, nil }
func (s *stubBroker) DeleteQueue(string) error                        { return nil }
func (s *stubBroker) DeprovisionQueue(string) error                   { return nil }
func (s *stubBroker) Subscribe(broker.Queue, string) error            { return nil }
func (s *stubBroker) Unsubscribe(broker.Queue, string) error          { return nil }
func (s *stubBroker) Receive(context.Context, broker.Queue) (*broker.Message, error) {
	return nil, nil
}
func (s *stubBroker) Request(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}

func TestInstrumentedBroker_PublishEmitsCanonicalHistogram(t *testing.T) {
	rec := installMetrics(t, NewRecordingMetrics())
	b := NewInstrumentedBroker(&stubBroker{})

	if err := b.Publish(t.Context(), "test/topic", &broker.Message{Payload: []byte("hi")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	hists := rec.Histograms("outbound.request.duration")
	if len(hists) != 1 {
		t.Fatalf("expected 1 histogram, got %d: %+v", len(hists), hists)
	}
	h := hists[0]
	if got := h.Labels["service.peer.name"]; got != "solace_broker" {
		t.Errorf("service.peer.name = %q, want solace_broker", got)
	}
	if got := h.Labels["operation.name"]; got != "publish" {
		t.Errorf("operation.name = %q, want publish", got)
	}
	if got := h.Labels["error.type"]; got != "" {
		t.Errorf("error.type = %q, want empty (success)", got)
	}
}

func TestInstrumentedBroker_PublishGuaranteedEmitsSameBucket(t *testing.T) {
	rec := installMetrics(t, NewRecordingMetrics())
	b := NewInstrumentedBroker(&stubBroker{})

	if err := b.Publish(t.Context(), "t", &broker.Message{Payload: []byte("a")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := b.PublishGuaranteed(t.Context(), "t", &broker.Message{Payload: []byte("b")}); err != nil {
		t.Fatalf("PublishGuaranteed: %v", err)
	}

	hists := rec.Histograms("outbound.request.duration")
	if len(hists) != 2 {
		t.Fatalf("expected 2 histograms, got %d", len(hists))
	}
	for i, h := range hists {
		if got := h.Labels["operation.name"]; got != "publish" {
			t.Errorf("histogram[%d].operation.name = %q, want publish", i, got)
		}
	}
}

func TestInstrumentedBroker_ClassifiesError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantErr string
	}{
		{"timeout", errors.New("publish timeout reached"), "timeout"},
		{"unauthorized", errors.New("unauthorized session"), "auth_error"},
		{"generic", errors.New("solace SDK rejected message"), "error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := installMetrics(t, NewRecordingMetrics())
			inner := &stubBroker{
				publishFn: func(context.Context, string, *broker.Message) error { return tc.err },
			}
			b := NewInstrumentedBroker(inner)

			if err := b.Publish(t.Context(), "t", &broker.Message{}); err == nil {
				t.Fatal("expected error to propagate")
			}

			hists := rec.Histograms("outbound.request.duration")
			if len(hists) != 1 {
				t.Fatalf("expected 1 histogram")
			}
			if got := hists[0].Labels["error.type"]; got != tc.wantErr {
				t.Errorf("error.type = %q, want %q", got, tc.wantErr)
			}
		})
	}
}

func TestInstrumentedBroker_DelegatesOtherMethods(t *testing.T) {
	rec := installMetrics(t, NewRecordingMetrics())
	b := NewInstrumentedBroker(&stubBroker{})

	if !b.IsConnected() {
		t.Error("IsConnected should delegate to stub (true)")
	}
	if err := b.Connect(t.Context()); err != nil {
		t.Errorf("Connect should delegate: %v", err)
	}

	if got := len(rec.Histograms("outbound.request.duration")); got != 0 {
		t.Errorf("non-publish methods leaked %d histograms", got)
	}
}

func TestInstrumentedBroker_ForwardsQueueProvisioningToTheBrokerItWraps(t *testing.T) {
	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	wrapped := NewInstrumentedBroker(inner)

	if err := broker.ProvisionQueue(t.Context(), wrapped, "q/held", broker.QueueOptions{Named: true}, "ns/spend"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if err := inner.PublishGuaranteed(t.Context(), "ns/spend", &broker.Message{Payload: []byte("x")}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := inner.Receive(ctx, &broker.SimpleQueue{QName: "q/held"}); err != nil {
		t.Errorf("the wrapped broker's queue was not provisioned: %v", err)
	}
}

type unboundBinder struct {
	*broker.DevBroker
}

func (unboundBinder) Bound(broker.Queue) bool { return false }

type withoutRequeue struct {
	broker.Broker
}

func TestTheInstrumentedBrokerIsABinderOnlyWhenTheBrokerItWrapsIs(t *testing.T) {
	if _, ok := NewInstrumentedBroker(broker.NewDevBroker()).(broker.Binder); ok {
		t.Fatal("a wrapped dev broker claims to be a broker.Binder")
	}
	wrapped, ok := NewInstrumentedBroker(unboundBinder{broker.NewDevBroker()}).(broker.Binder)
	if !ok {
		t.Fatal("a wrapped broker.Binder is not one")
	}
	if wrapped.Bound(&broker.SimpleQueue{QName: "q"}) {
		t.Fatal("bind state is not forwarded through the instrumented broker")
	}
}

func TestTheInstrumentedBrokerForwardsRequeue(t *testing.T) {
	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	q, err := inner.CreateQueue("q/requeue", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := NewInstrumentedBroker(inner)
	if err = broker.Requeue(wrapped, "q/requeue", &broker.Message{Payload: []byte("again")}); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	m, err := inner.Receive(ctx, q)
	if err != nil || string(m.Payload) != "again" || !m.Redelivered {
		t.Fatalf("received %+v, %v; want the message again, marked redelivered", m, err)
	}
	if err = broker.Requeue(NewInstrumentedBroker(withoutRequeue{broker.NewDevBroker()}), "q", &broker.Message{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("requeue on a broker without it = %v, want ErrUnsupported", err)
	}
}
