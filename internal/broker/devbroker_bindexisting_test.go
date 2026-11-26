// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDevBrokerBindExisting(t *testing.T) {
	t.Parallel()
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if _, err := b.BindExisting("ns/q/old"); !errors.Is(err, ErrQueueNotFound) {
		t.Fatalf("BindExisting on a missing queue: err = %v, want ErrQueueNotFound", err)
	}
	if b.HasQueue("ns/q/old") {
		t.Fatal("BindExisting created the missing queue")
	}

	q, err := b.CreateQueue("ns/q/old", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := b.Subscribe(q, "ns/configuration/model/>"); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	bound, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting on an existing queue: %v", err)
	}
	if err := bound.RemoveSubscription("ns/configuration/model/>"); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}
	bound.Close()

	if err := b.Publish(t.Context(), "ns/configuration/model/general", &Message{Payload: []byte("x")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if msg, err := b.Receive(ctx, q); err == nil {
		t.Fatalf("queue still received %q after its subscription was removed", msg.Topic)
	}
}

func TestDevBoundQueueReceive(t *testing.T) {
	t.Parallel()
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	q, err := b.CreateQueue("ns/q/old", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if subErr := b.Subscribe(q, "ns/in/>"); subErr != nil {
		t.Fatalf("Subscribe: %v", subErr)
	}
	for _, p := range []string{"m1", "m2"} {
		if pubErr := b.Publish(t.Context(), "ns/in/x", &Message{Payload: []byte(p)}); pubErr != nil {
			t.Fatalf("Publish: %v", pubErr)
		}
	}
	bound, err := b.BindExisting("ns/q/old")
	if err != nil {
		t.Fatalf("BindExisting: %v", err)
	}
	defer bound.Close()

	for _, want := range []string{"m1", "m2"} {
		msg, err := bound.Receive(t.Context())
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		if string(msg.Payload) != want || msg.Topic != "ns/in/x" {
			t.Fatalf("Receive = %q on %q, want %q on ns/in/x", msg.Payload, msg.Topic, want)
		}
		msg.Ack()
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := bound.Receive(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Receive on an empty queue: err = %v, want DeadlineExceeded", err)
	}

	if err := b.DeprovisionQueue("ns/q/old"); err != nil {
		t.Fatalf("DeprovisionQueue: %v", err)
	}
	if _, err := bound.Receive(t.Context()); !errors.Is(err, ErrQueueNotFound) {
		t.Fatalf("Receive after the queue was deleted: err = %v, want ErrQueueNotFound", err)
	}
}
