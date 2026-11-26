// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestMessage_OnlyDevBrokerDeliveriesSayTheirNackDoesNotRedeliver(t *testing.T) {
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = b.Disconnect(context.Background()) })
	q, err := b.CreateQueue("nack-capability", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = b.Subscribe(q, "nack/capability"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err = b.PublishGuaranteed(t.Context(), "nack/capability", &Message{Payload: []byte("x")}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := b.Receive(ctx, q)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if msg.NackRedelivers() {
		t.Error("a dev broker delivery claims its Nack redelivers")
	}
	if NewDeliveredMessage("id", "topic", nil, func() {}, func() {}).NackRedelivers() {
		t.Error("a delivery that did not say its Nack redelivers claims it does")
	}
	if !NewRedeliverableMessage("id", "topic", nil, func() {}, func() {}).NackRedelivers() {
		t.Error("a delivery built as redeliverable says it cannot redeliver")
	}
}

func TestNetworkDevBroker_DeliveriesSayTheirNackDoesNotRedeliver(t *testing.T) {
	c := NewNetworkDevBroker("127.0.0.1:0", "id", "q", slog.Default())
	first := newNetQueueForTest("first", "evt/>", 4)
	second := newNetQueueForTest("second", "evt/>", 4)
	c.queues["first"], c.queues["second"] = first, second

	c.dispatchMessage(&NetworkMessage{MessageID: "m1", Topic: "evt/test", Payload: "x"})

	for name, q := range map[string]*netQueue{"first": first, "second": second} {
		select {
		case got := <-q.msgCh:
			if got.NackRedelivers() {
				t.Errorf("queue %s: a network dev delivery claims its Nack redelivers", name)
			}
		default:
			t.Fatalf("queue %s received nothing", name)
		}
	}
}
