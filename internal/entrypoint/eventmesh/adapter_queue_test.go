// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type queueTrackingBroker struct {
	broker.Broker
	mu             sync.Mutex
	createdQueues  []string
	deletedQueues  []string
	subscriptions  []string
	subscribeErrOn string
	createQueueErr error
	deleteQueueErr error
	createAttempts int
	connects       int
	disconnects    int
}

func (b *queueTrackingBroker) DeprovisionQueue(string) error { return nil }

func (b *queueTrackingBroker) Connect(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.connects++
	return nil
}

func (b *queueTrackingBroker) Disconnect(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disconnects++
	return nil
}

func (b *queueTrackingBroker) IsConnected() bool { return true }

func (b *queueTrackingBroker) CreateQueue(name string, _ broker.QueueOptions) (broker.Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.createAttempts++
	if b.createQueueErr != nil {
		return nil, b.createQueueErr
	}
	b.createdQueues = append(b.createdQueues, name)
	return &broker.SimpleQueue{QName: name}, nil
}

func (b *queueTrackingBroker) Subscribe(_ broker.Queue, topic string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subscribeErrOn != "" && topic == b.subscribeErrOn {
		return errors.New("simulated subscribe failure")
	}
	b.subscriptions = append(b.subscriptions, topic)
	return nil
}

func (b *queueTrackingBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deletedQueues = append(b.deletedQueues, name)
	return b.deleteQueueErr
}

func (b *queueTrackingBroker) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func newAdapterForQueueTest(brk broker.Broker, handlers []*EventHandlerConfig) *EventMeshAdapter {
	a := &EventMeshAdapter{
		logger:     slog.Default(),
		cfg:        &Config{EventHandlers: handlers},
		dataBroker: brk,
	}
	a.ensureIngress()
	a.core = entrypoint.NewGatewayCore(a, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "gw-1",
	})
	return a
}

func TestEventMeshAdapter_Start_CreatesDurableQueuePerHandler(t *testing.T) {
	brk := &queueTrackingBroker{}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "ha", Subscriptions: []SubscriptionConfig{{Topic: "in/a"}}},
		{Name: "hb", Subscriptions: []SubscriptionConfig{{Topic: "in/b"}}},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	waitFor(t, func() bool {
		brk.mu.Lock()
		defer brk.mu.Unlock()
		return len(brk.createdQueues) >= 2
	})

	brk.mu.Lock()
	defer brk.mu.Unlock()
	want := []string{
		"test-ns/q/eventmesh/gw-1/ha",
		"test-ns/q/eventmesh/gw-1/hb",
	}
	if len(brk.createdQueues) != len(want) {
		t.Fatalf("createdQueues = %v, want %v", brk.createdQueues, want)
	}
	for i, name := range want {
		if brk.createdQueues[i] != name {
			t.Errorf("createdQueues[%d] = %q, want %q", i, brk.createdQueues[i], name)
		}
	}
}

func TestEventMeshAdapter_Stop_DeletesAllHandlerQueues(t *testing.T) {
	brk := &queueTrackingBroker{}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "ha", Subscriptions: []SubscriptionConfig{{Topic: "in/a"}}},
		{Name: "hb", Subscriptions: []SubscriptionConfig{{Topic: "in/b"}}},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		brk.mu.Lock()
		defer brk.mu.Unlock()
		return len(brk.createdQueues) >= 2
	})
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	brk.mu.Lock()
	defer brk.mu.Unlock()
	want := []string{
		"test-ns/q/eventmesh/gw-1/ha",
		"test-ns/q/eventmesh/gw-1/hb",
	}
	if len(brk.deletedQueues) != len(want) {
		t.Fatalf("deletedQueues = %v, want %v", brk.deletedQueues, want)
	}
}

func TestEventMeshAdapter_Start_CleansUpOnSubscribeFailure(t *testing.T) {
	brk := &queueTrackingBroker{subscribeErrOn: "in/b"}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "ha", Subscriptions: []SubscriptionConfig{{Topic: "in/a"}}},
		{Name: "hb", Subscriptions: []SubscriptionConfig{{Topic: "in/b"}}},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v; an event broker problem must not fail the start", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	waitFor(t, func() bool { return len(a.health.Snapshot()) > 0 })

	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.deletedQueues) < 2 {
		t.Fatalf("expected both pre-failure queues to be deleted; deletedQueues = %v",
			brk.deletedQueues)
	}
	if brk.disconnects != brk.connects {
		t.Fatalf("%d sessions opened, %d closed: a failed attempt left its session open", brk.connects, brk.disconnects)
	}
}

func TestEventMeshAdapter_Start_DegradesOnCreateQueueError(t *testing.T) {
	brk := &queueTrackingBroker{createQueueErr: errors.New("broker rejected")}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "ha", Subscriptions: []SubscriptionConfig{{Topic: "in/a"}}},
	})
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v; an event broker problem must not fail the start", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	waitFor(t, func() bool { return len(a.health.Snapshot()) > 0 })

	_ = a.DeprovisionAdapterQueues(context.Background())
	brk.mu.Lock()
	attempts := brk.createAttempts
	brk.mu.Unlock()
	time.Sleep(2500 * time.Millisecond)
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if brk.createAttempts > attempts+1 {
		t.Fatalf("a removed entrypoint kept retrying its event broker: %d attempts after removal", brk.createAttempts-attempts)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEventMeshAdapter_Stop_LogsOnDeleteQueueError(t *testing.T) {
	brk := &queueTrackingBroker{deleteQueueErr: errors.New("broker timeout")}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "ha", Subscriptions: []SubscriptionConfig{{Topic: "in/a"}}},
		{Name: "hb", Subscriptions: []SubscriptionConfig{{Topic: "in/b"}}},
	})
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error despite per-queue errors being logged: %v", err)
	}
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.deletedQueues) != 2 {
		t.Errorf("Stop must attempt DeleteQueue for every handler queue even when earlier deletes fail; got %v",
			brk.deletedQueues)
	}
}
