// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type consumerBinderBroker struct {
	queueTrackingBroker
	consumeErr      error
	consumedQueues  []string
	consumeBinderMu sync.Mutex
}

func (b *consumerBinderBroker) ConsumeExisting(name string) (broker.Queue, error) {
	b.consumeBinderMu.Lock()
	defer b.consumeBinderMu.Unlock()
	if b.consumeErr != nil {
		return nil, b.consumeErr
	}
	b.consumedQueues = append(b.consumedQueues, name)
	return &broker.SimpleQueue{QName: name}, nil
}

func (b *consumerBinderBroker) BindExisting(_ string) (broker.BoundQueue, error) {
	return nil, errors.New("unexpected BindExisting call: Start uses ConsumeExisting for consume-only queues")
}

func (b *consumerBinderBroker) consumed() []string {
	b.consumeBinderMu.Lock()
	defer b.consumeBinderMu.Unlock()
	out := make([]string, len(b.consumedQueues))
	copy(out, b.consumedQueues)
	return out
}

func TestEventMeshAdapter_Start_ExistingQueueName_BindsConsumeOnlyNeverCreatesQueue(t *testing.T) {
	brk := &consumerBinderBroker{}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "orders", ExistingQueueName: "preprov/q/orders"},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	waitForCondition(t, func() bool {
		got := brk.consumed()
		return len(got) == 1 && got[0] == "preprov/q/orders"
	})
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.createdQueues) != 0 {
		t.Errorf("CreateQueue must not fire for an existing_queue_name handler; got %v", brk.createdQueues)
	}
	if len(brk.subscriptions) != 0 {
		t.Errorf("Subscribe must not fire for an existing_queue_name handler; got %v", brk.subscriptions)
	}
}

func TestEventMeshAdapter_Start_MixedHandlers_DispatchesPerHandler(t *testing.T) {
	brk := &consumerBinderBroker{}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "orders", ExistingQueueName: "preprov/q/orders"},
		{Name: "events", Subscriptions: []SubscriptionConfig{{Topic: "in/events"}}},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	waitForCondition(t, func() bool {
		got := brk.consumed()
		return len(got) == 1 && got[0] == "preprov/q/orders"
	})
	brk.mu.Lock()
	defer brk.mu.Unlock()
	wantCreated := []string{"test-ns/q/eventmesh/gw-1/events"}
	if len(brk.createdQueues) != 1 || brk.createdQueues[0] != wantCreated[0] {
		t.Errorf("CreateQueue = %v, want %v (only the subscription handler)", brk.createdQueues, wantCreated)
	}
	if len(brk.subscriptions) != 1 || brk.subscriptions[0] != "in/events" {
		t.Errorf("Subscribe = %v, want [in/events] (only the subscription handler)", brk.subscriptions)
	}
}

func TestEventMeshAdapter_Start_ExistingQueueName_BindFailureIsNotFatalToSibling(t *testing.T) {
	brk := &consumerBinderBroker{consumeErr: errors.New("queue not found")}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "orders", ExistingQueueName: "preprov/q/missing"},
		{Name: "events", Subscriptions: []SubscriptionConfig{{Topic: "in/events"}}},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start must succeed even when a consume-only handler cannot bind; got: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	waitForCondition(t, func() bool {
		for _, p := range a.HealthProblems() {
			if p.ID == "queue:preprov/q/missing" && p.Severity == health.Degraded {
				return true
			}
		}
		return false
	})
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if got, want := brk.createdQueues, []string{"test-ns/q/eventmesh/gw-1/events"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("sibling SAM-owned queue not created or extraneous queue created: got %v, want %v", got, want)
	}
	if len(brk.deletedQueues) != 0 {
		t.Errorf("sibling SAM-owned queue must not be deleted while adapter runs; deletedQueues = %v", brk.deletedQueues)
	}
}

func TestEventMeshAdapter_Start_ExistingQueueName_RebindsAfterTransientFailure(t *testing.T) {
	prev := bindRetryInitialDelay
	bindRetryInitialDelay = 5 * time.Millisecond
	t.Cleanup(func() { bindRetryInitialDelay = prev })

	brk := &transientFailBinderBroker{failUntil: 2}
	a := newAdapterForQueueTest(brk, []*EventHandlerConfig{
		{Name: "orders", ExistingQueueName: "preprov/q/orders"},
	})

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	waitForCondition(t, func() bool {
		return brk.calls() >= 1 && len(a.HealthProblems()) > 0
	})
	waitForCondition(t, func() bool {
		return len(a.HealthProblems()) == 0
	})
}

type transientFailBinderBroker struct {
	queueTrackingBroker
	mu        sync.Mutex
	failUntil int
	callCount int
}

func (b *transientFailBinderBroker) ConsumeExisting(name string) (broker.Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.callCount++
	if b.callCount <= b.failUntil {
		return nil, errors.New("transient bind failure")
	}
	return &broker.SimpleQueue{QName: name}, nil
}

func (b *transientFailBinderBroker) BindExisting(_ string) (broker.BoundQueue, error) {
	return nil, errors.New("unexpected BindExisting call")
}

func (b *transientFailBinderBroker) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.callCount
}

func TestEventMeshAdapter_DeprovisionAdapterQueues_LeavesCustomerQueueUntouched(t *testing.T) {
	dataBroker := &deprovisionTrackingBroker{DevBroker: broker.NewDevBroker()}
	if err := dataBroker.Connect(context.Background()); err != nil {
		t.Fatalf("connect data broker: %v", err)
	}

	customerQueue := "preprov/q/orders"
	owned, err := dataBroker.CreateQueue(customerQueue, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("pre-create customer queue: %v", err)
	}
	if err := dataBroker.Subscribe(owned, "preprov/evt/>"); err != nil {
		t.Fatalf("pre-subscribe customer queue: %v", err)
	}

	a := newAdapterForQueueTest(dataBroker, []*EventHandlerConfig{
		{Name: "orders", ExistingQueueName: customerQueue},
		{Name: "events", Subscriptions: []SubscriptionConfig{{Topic: "in/events"}}},
	})

	if err := a.DeprovisionAdapterQueues(context.Background()); err != nil {
		t.Fatalf("DeprovisionAdapterQueues: %v", err)
	}

	if !dataBroker.HasQueue(customerQueue) {
		t.Errorf("customer queue %q must survive DeprovisionAdapterQueues", customerQueue)
	}
	for _, name := range dataBroker.names() {
		if name == customerQueue {
			t.Errorf("Deprovision called on customer queue %q; adapter must only touch SAM-derived names", customerQueue)
		}
	}
}

func TestEventMeshAdapter_ExistingQueueName_RealDevBroker_Lifecycle(t *testing.T) {
	dataBroker := broker.NewDevBroker()
	if err := dataBroker.Connect(context.Background()); err != nil {
		t.Fatalf("connect data broker: %v", err)
	}

	customerQueue := "preprov/q/trades"
	owned, err := dataBroker.CreateQueue(customerQueue, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("pre-create customer queue: %v", err)
	}
	if err = dataBroker.Subscribe(owned, "preprov/evt/trades/>"); err != nil {
		t.Fatalf("pre-subscribe customer queue: %v", err)
	}

	a := newAdapterForQueueTest(dataBroker, []*EventHandlerConfig{
		{Name: "trades", ExistingQueueName: customerQueue},
	})

	if err = a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })

	waitForCondition(t, func() bool {
		return len(a.HealthProblems()) == 0
	})

	shadow := "test-ns/q/eventmesh/gw-1/trades"
	if dataBroker.HasQueue(shadow) {
		t.Errorf("adapter created a shadow queue %q for an existing_queue_name handler", shadow)
	}

	if err = a.DeprovisionAdapterQueues(context.Background()); err != nil {
		t.Fatalf("DeprovisionAdapterQueues: %v", err)
	}

	a.cancel()
	a.wg.Wait()

	if !dataBroker.HasQueue(customerQueue) {
		t.Fatalf("customer queue %q removed by undeploy; must survive", customerQueue)
	}
	if err = dataBroker.Publish(context.Background(), "preprov/evt/trades/us", &broker.Message{Payload: []byte("post-undeploy")}); err != nil {
		t.Fatalf("publish after undeploy: %v", err)
	}
	q, err := dataBroker.ConsumeExisting(customerQueue)
	if err != nil {
		t.Fatalf("re-ConsumeExisting after undeploy: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := dataBroker.Receive(ctx, q)
	if err != nil {
		t.Fatalf("Receive after undeploy: %v", err)
	}
	if got, want := string(msg.Payload), "post-undeploy"; got != want {
		t.Fatalf("post-undeploy payload = %q, want %q", got, want)
	}
}
