// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"testing"
	"time"
)

func TestProvisionQueue_OnTheDevBrokerAMessagePublishedBeforeAnyConsumerWaitsForIt(t *testing.T) {
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	opts := QueueOptions{Named: true}

	if err := ProvisionQueue(t.Context(), b, "q/held", opts, "ns/spend"); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if err := b.PublishGuaranteed(t.Context(), "ns/spend", &Message{Payload: []byte("x")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	q, err := b.CreateQueue("q/held", opts)
	if err != nil {
		t.Fatalf("consumer creates the queue: %v", err)
	}
	if err := b.Subscribe(q, "ns/spend"); err != nil {
		t.Fatalf("consumer subscribes: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if msg, err := b.Receive(ctx, q); err != nil || string(msg.Payload) != "x" {
		t.Fatalf("receive = %v, %v; want the message published before the consumer started, once", msg, err)
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel2()
	if msg, err := b.Receive(ctx2, q); err == nil {
		t.Errorf("received %q again: provisioning and the consumer subscribing must not deliver twice", msg.Payload)
	}
}

type plainBroker struct{ Broker }

func TestProvisionQueue_ABrokerThatCannotHoldMessagesForAnAbsentConsumerIsLeftAlone(t *testing.T) {
	if err := ProvisionQueue(t.Context(), plainBroker{}, "q", QueueOptions{Named: true}, "t"); err != nil {
		t.Errorf("provision on a broker without QueueProvisioner = %v, want nil", err)
	}
}
