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

func TestDeliveredMessageAckNack(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("ack-q", QueueOptions{})
	must(t, b.Subscribe(q, "ack/topic"))
	must(t, b.Publish(ctx, "ack/topic", &Message{Payload: []byte("data")}))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(timeoutCtx, q)
	must(t, err)

	msg.Ack()
	msg.Nack()

	if msg.ackFunc == nil {
		t.Error("delivered message should have a non-nil ackFunc")
	}
	if msg.nackFunc == nil {
		t.Error("delivered message should have a non-nil nackFunc")
	}
}

func TestAckNackOnOutgoingMessageNoOp(t *testing.T) {
	msg := &Message{Payload: []byte("outgoing")}
	msg.Ack()
	msg.Nack()
}

func TestPublishNilPayload(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("nil-payload-q", QueueOptions{})
	must(t, b.Subscribe(q, "nil/payload"))

	must(t, b.Publish(ctx, "nil/payload", &Message{Payload: nil}))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(timeoutCtx, q)
	must(t, err)

	if msg.Payload != nil {
		t.Errorf("expected nil payload, got %v", msg.Payload)
	}
}

func TestPublishNilUserProperties(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("nil-props-q", QueueOptions{})
	must(t, b.Subscribe(q, "nil/props"))

	must(t, b.Publish(ctx, "nil/props", &Message{
		Payload:        []byte("data"),
		UserProperties: nil,
	}))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(timeoutCtx, q)
	must(t, err)

	if msg.UserProperties != nil {
		t.Errorf("expected nil UserProperties, got %v", msg.UserProperties)
	}
}

func TestRequestNoResponderTimeout(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	request := &Message{
		Payload: []byte("lonely request"),
		ReplyTo: "reply/nobody",
	}
	_, err := b.Request(ctx, "nowhere/topic", request, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error when no responder exists")
	}
	if err != context.DeadlineExceeded {
		t.Logf("got error: %v (acceptable — wraps deadline exceeded)", err)
	}
}

func TestRequestMissingReplyTo(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	request := &Message{
		Payload: []byte("no reply-to"),
	}
	_, err := b.Request(ctx, "some/topic", request, time.Second)
	if err == nil {
		t.Fatal("expected error when ReplyTo is empty")
	}
}

func TestUnsubscribeNonexistentSubscription(t *testing.T) {
	b := newConnectedBroker(t)

	q, _ := b.CreateQueue("unsub-noop-q", QueueOptions{})
	err := b.Unsubscribe(q, "never/subscribed")
	if err != nil {
		t.Errorf("unsubscribe of nonexistent subscription should not error, got %v", err)
	}
}

func TestDeleteQueueCleansUpSubscriptions(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("cleanup-q", QueueOptions{})
	must(t, b.Subscribe(q, "cleanup/>"))

	must(t, b.DeleteQueue("cleanup-q"))

	err := b.Publish(ctx, "cleanup/test", &Message{Payload: []byte("orphan")})
	if err != nil {
		t.Errorf("publish after queue delete should not error, got %v", err)
	}
}

func TestReceiveOnNonexistentQueue(t *testing.T) {
	b := newConnectedBroker(t)

	fakeQueue := &SimpleQueue{QName: "does-not-exist"}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := b.Receive(timeoutCtx, fakeQueue)
	if err == nil {
		t.Fatal("expected error when receiving from nonexistent queue")
	}
}

func TestReceiveContextCancelled(t *testing.T) {
	b := newConnectedBroker(t)

	q, _ := b.CreateQueue("cancel-q", QueueOptions{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := b.Receive(ctx, q)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestDuplicateSubscribeIsIdempotent(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("dup-sub-q", QueueOptions{})
	must(t, b.Subscribe(q, "dup/topic"))
	must(t, b.Subscribe(q, "dup/topic"))

	must(t, b.Publish(ctx, "dup/topic", &Message{Payload: []byte("once")}))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(timeoutCtx, q)
	must(t, err)
	if msg == nil {
		t.Fatal("should receive one message")
	}

	timeoutCtx2, cancel2 := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel2()
	msg2, _ := b.Receive(timeoutCtx2, q)
	if msg2 != nil {
		t.Error("should not receive a duplicate message from double subscribe")
	}
}

func TestCloneMessagePreservesReplyToAndCorrelationID(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("clone-q", QueueOptions{})
	must(t, b.Subscribe(q, "clone/test"))

	must(t, b.Publish(ctx, "clone/test", &Message{
		Payload:       []byte("data"),
		ReplyTo:       "reply/here",
		CorrelationID: "corr-42",
	}))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(timeoutCtx, q)
	must(t, err)

	if msg.ReplyTo != "reply/here" {
		t.Errorf("ReplyTo = %q, want %q", msg.ReplyTo, "reply/here")
	}
	if msg.CorrelationID != "corr-42" {
		t.Errorf("CorrelationID = %q, want %q", msg.CorrelationID, "corr-42")
	}
}

func TestNewDeliveredMessage(t *testing.T) {
	acked := false
	nacked := false
	msg := NewDeliveredMessage("id-1", "some/topic", []byte("payload"),
		func() { acked = true },
		func() { nacked = true },
	)

	if msg.ID != "id-1" {
		t.Errorf("ID = %q, want %q", msg.ID, "id-1")
	}
	if msg.Topic != "some/topic" {
		t.Errorf("Topic = %q, want %q", msg.Topic, "some/topic")
	}
	if string(msg.Payload) != "payload" {
		t.Errorf("Payload = %q, want %q", msg.Payload, "payload")
	}

	msg.Ack()
	if !acked {
		t.Error("Ack callback was not invoked")
	}

	msg.Nack()
	if !nacked {
		t.Error("Nack callback was not invoked")
	}
}

func TestCreateQueueIdempotent(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q1, err := b.CreateQueue("idem-q", QueueOptions{})
	must(t, err)
	q2, err := b.CreateQueue("idem-q", QueueOptions{})
	must(t, err)

	must(t, b.Subscribe(q1, "idem/topic"))

	must(t, b.Publish(ctx, "idem/topic", &Message{Payload: []byte("shared")}))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(timeoutCtx, q2)
	must(t, err)
	if string(msg.Payload) != "shared" {
		t.Errorf("Payload = %q, want %q", msg.Payload, "shared")
	}
}

func TestTemporaryQueuesHaveUniqueNames(t *testing.T) {
	b := newConnectedBroker(t)

	q1, _ := b.CreateDirectSubscription()
	q2, _ := b.CreateDirectSubscription()

	if q1.Name() == q2.Name() {
		t.Errorf("temporary queues should have unique names, both got %q", q1.Name())
	}
}
