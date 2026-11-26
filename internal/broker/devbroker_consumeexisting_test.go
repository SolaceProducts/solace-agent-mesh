// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newConsumeExistingBroker(t *testing.T, queueName string) *DevBroker {
	t.Helper()
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if queueName == "" {
		return b
	}
	owned, err := b.CreateQueue(queueName, QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := b.Subscribe(owned, "preprov/evt/>"); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return b
}

func TestDevBrokerConsumeExisting_MissingQueueFails(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "")

	if _, err := b.ConsumeExisting("ns/q/pre"); !errors.Is(err, ErrQueueNotFound) {
		t.Fatalf("ConsumeExisting on a missing queue: err = %v, want ErrQueueNotFound", err)
	}
	if b.HasQueue("ns/q/pre") {
		t.Fatal("ConsumeExisting created the missing queue")
	}
}

func TestDevBrokerConsumeExisting_ReceivesMessages(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/entrypoint-test")

	q, err := b.ConsumeExisting("preprov/q/entrypoint-test")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	if err = b.Publish(t.Context(), "preprov/evt/trade", &Message{Payload: []byte("hello")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

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

func TestDevBrokerConsumeExisting_DeprovisionAndDeleteAreNoOps(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/kept")

	if _, err := b.ConsumeExisting("preprov/q/kept"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	if err := b.DeprovisionQueue("preprov/q/kept"); err != nil {
		t.Fatalf("DeprovisionQueue: %v", err)
	}
	if !b.HasQueue("preprov/q/kept") {
		t.Fatal("DeprovisionQueue removed a consume-only queue")
	}

	if err := b.DeleteQueue("preprov/q/kept"); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	if !b.HasQueue("preprov/q/kept") {
		t.Fatal("DeleteQueue removed a consume-only queue")
	}

	if err := b.Publish(t.Context(), "preprov/evt/x", &Message{Payload: []byte("after")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	q, err := b.ConsumeExisting("preprov/q/kept")
	if err != nil {
		t.Fatalf("ConsumeExisting after undeploy: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if msg, err := b.Receive(ctx, q); err != nil || string(msg.Payload) != "after" {
		t.Fatalf("Receive after undeploy: msg = %+v err = %v", msg, err)
	}
}

func TestDevBrokerConsumeExisting_CompetingConsumers(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/shared")

	q1, err := b.ConsumeExisting("preprov/q/shared")
	if err != nil {
		t.Fatalf("ConsumeExisting 1: %v", err)
	}
	q2, err := b.ConsumeExisting("preprov/q/shared")
	if err != nil {
		t.Fatalf("ConsumeExisting 2: %v", err)
	}

	const total = 10
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var c1, c2 atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)
	drain := func(q Queue, got *atomic.Int32) {
		defer wg.Done()
		for {
			rCtx, rCancel := context.WithTimeout(ctx, 200*time.Millisecond)
			msg, err := b.Receive(rCtx, q)
			rCancel()
			if err != nil || msg == nil {
				return
			}
			got.Add(1)
		}
	}
	go drain(q1, &c1)
	go drain(q2, &c2)

	for i := 0; i < total; i++ {
		if err := b.Publish(t.Context(), "preprov/evt/x", &Message{Payload: []byte("m")}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	wg.Wait()

	if c1.Load()+c2.Load() != total {
		t.Fatalf("received %d+%d = %d, want %d", c1.Load(), c2.Load(), c1.Load()+c2.Load(), total)
	}
}

func TestDevBrokerConsumeExisting_NotConnected(t *testing.T) {
	t.Parallel()
	b := NewDevBroker()
	if _, err := b.ConsumeExisting("preprov/q/x"); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("ConsumeExisting before Connect: err = %v, want 'not connected'", err)
	}
}

func TestDevBrokerClientConsumeExisting_Closed(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/x")

	c := b.NewClient()
	if err := c.Connect(t.Context()); err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	if err := c.Disconnect(t.Context()); err != nil {
		t.Fatalf("client Disconnect: %v", err)
	}
	if _, err := c.ConsumeExisting("preprov/q/x"); !errors.Is(err, errDevClientClosed) {
		t.Fatalf("ConsumeExisting on closed client: err = %v, want errDevClientClosed", err)
	}
}

func TestDevBrokerConsumeExisting_SubscribeRefused(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/x")

	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	err = b.Subscribe(q, "preprov/evt/>")
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("Subscribe on a consume-only handle must fail with 'consume-only'; got %v", err)
	}
}

func TestDevBrokerConsumeExisting_CreateQueueRefused(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/x")
	if _, err := b.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	_, err := b.CreateQueue("preprov/q/x", QueueOptions{Named: true})
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("CreateQueue on consume-only name: err = %v, want 'consume-only'", err)
	}
}

func TestDevBrokerConsumeExisting_ProvisionQueueRefused(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/x")
	if _, err := b.ConsumeExisting("preprov/q/x"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}
	err := b.ProvisionQueue(t.Context(), "preprov/q/x", QueueOptions{Named: true}, "preprov/evt/>")
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("ProvisionQueue on consume-only name: err = %v, want 'consume-only'", err)
	}
}

func TestDevBrokerConsumeExisting_UnsubscribeRefused(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/x")

	q, err := b.ConsumeExisting("preprov/q/x")
	if err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	err = b.Unsubscribe(q, "preprov/evt/>")
	if err == nil || !strings.Contains(err.Error(), "consume-only") {
		t.Fatalf("Unsubscribe on a consume-only handle must fail with 'consume-only'; got %v", err)
	}
}

func TestDevBrokerConsumeExisting_ExclusiveQueueRefused(t *testing.T) {
	t.Parallel()
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, err := b.CreateQueue("preprov/q/ex", QueueOptions{Named: true, Exclusive: true}); err != nil {
		t.Fatalf("CreateQueue exclusive: %v", err)
	}

	_, err := b.ConsumeExisting("preprov/q/ex")
	if err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("ConsumeExisting on an exclusive queue must fail with 'exclusive'; got %v", err)
	}
}

func TestDevBrokerConsumeExisting_NoSubscriptionAdded(t *testing.T) {
	t.Parallel()
	b := newConsumeExistingBroker(t, "preprov/q/pre")

	b.mu.RLock()
	before := len(b.subscriptions)
	b.mu.RUnlock()

	if _, err := b.ConsumeExisting("preprov/q/pre"); err != nil {
		t.Fatalf("ConsumeExisting: %v", err)
	}

	b.mu.RLock()
	after := len(b.subscriptions)
	b.mu.RUnlock()

	if before != after {
		t.Fatalf("subscription count changed: before=%d after=%d", before, after)
	}
}
