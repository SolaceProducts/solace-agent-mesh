// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newConnectedBroker(t *testing.T) *DevBroker {
	t.Helper()
	b := NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestDevBrokerConnectDisconnect(t *testing.T) {
	b := NewDevBroker()
	if b.IsConnected() {
		t.Fatal("should not be connected initially")
	}

	must(t, b.Connect(context.Background()))
	if !b.IsConnected() {
		t.Fatal("should be connected after Connect")
	}

	must(t, b.Disconnect(context.Background()))
	if b.IsConnected() {
		t.Fatal("should not be connected after Disconnect")
	}
}

func TestDevBrokerPubSub(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, err := b.CreateQueue("test-q", QueueOptions{})
	must(t, err)
	must(t, b.Subscribe(q, "test/topic"))

	msg := &Message{
		Payload:        []byte(`{"hello":"world"}`),
		UserProperties: map[string]string{"key": "value"},
	}
	must(t, b.Publish(ctx, "test/topic", msg))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	received, err := b.Receive(timeoutCtx, q)
	must(t, err)

	if received.Topic != "test/topic" {
		t.Errorf("topic = %q, want %q", received.Topic, "test/topic")
	}
	if string(received.Payload) != `{"hello":"world"}` {
		t.Errorf("payload = %q", received.Payload)
	}
	if received.UserProperties["key"] != "value" {
		t.Errorf("user properties = %v", received.UserProperties)
	}
}

func TestDevBrokerWildcardSub(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("wildcard-q", QueueOptions{})
	must(t, b.Subscribe(q, "sam/agents/>"))

	tests := []struct {
		topic   string
		matches bool
	}{
		{"sam/agents/web-agent/request", true},
		{"sam/agents/tool-agent/status", true},
		{"sam/gateways/slack", false},
	}

	for _, tt := range tests {
		msg := &Message{Payload: []byte("test")}
		must(t, b.Publish(ctx, tt.topic, msg))

		timeoutCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		received, err := b.Receive(timeoutCtx, q)
		cancel()

		if tt.matches {
			if err != nil || received == nil {
				t.Errorf("topic %q should match, got err=%v", tt.topic, err)
			} else if received.Topic != tt.topic {
				t.Errorf("received topic = %q, want %q", received.Topic, tt.topic)
			}
		} else {
			if received != nil {
				t.Errorf("topic %q should NOT match, got message", tt.topic)
			}
		}
	}
}

func TestDevBrokerMultipleSubscribers(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q1, _ := b.CreateQueue("q1", QueueOptions{})
	q2, _ := b.CreateQueue("q2", QueueOptions{})
	must(t, b.Subscribe(q1, "broadcast/>"))
	must(t, b.Subscribe(q2, "broadcast/>"))

	msg := &Message{Payload: []byte("hello")}
	must(t, b.Publish(ctx, "broadcast/all", msg))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	r1, err := b.Receive(timeoutCtx, q1)
	if err != nil || r1 == nil {
		t.Fatalf("q1 should receive, err=%v", err)
	}
	r2, err := b.Receive(timeoutCtx, q2)
	if err != nil || r2 == nil {
		t.Fatalf("q2 should receive, err=%v", err)
	}

	r1.Payload[0] = 'X'
	if r2.Payload[0] == 'X' {
		t.Error("messages should be independent copies")
	}
}

func TestDevBrokerUnsubscribe(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("unsub-q", QueueOptions{})
	must(t, b.Subscribe(q, "topic/a"))

	must(t, b.Publish(ctx, "topic/a", &Message{Payload: []byte("1")}))
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	msg, _ := b.Receive(timeoutCtx, q)
	cancel()
	if msg == nil {
		t.Fatal("should receive before unsubscribe")
	}

	must(t, b.Unsubscribe(q, "topic/a"))

	must(t, b.Publish(ctx, "topic/a", &Message{Payload: []byte("2")}))
	timeoutCtx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
	msg, _ = b.Receive(timeoutCtx, q)
	cancel()
	if msg != nil {
		t.Error("should not receive after unsubscribe")
	}
}

func TestDevBrokerPublishNotConnected(t *testing.T) {
	b := NewDevBroker()
	err := b.Publish(context.Background(), "topic", &Message{})
	if err == nil {
		t.Fatal("should error when not connected")
	}
}

func TestDevBrokerRequestReply(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	reqQueue, _ := b.CreateQueue("responder-q", QueueOptions{})
	must(t, b.Subscribe(reqQueue, "request/topic"))

	go func() {
		timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, err := b.Receive(timeoutCtx, reqQueue)
		if err != nil || req == nil {
			return
		}
		reply := &Message{
			Payload:       []byte("REPLY:" + string(req.Payload)),
			CorrelationID: req.CorrelationID,
		}
		_ = b.Publish(ctx, req.ReplyTo, reply)
	}()

	request := &Message{
		Payload: []byte("PING"),
		ReplyTo: "reply/corr-001",
	}
	reply, err := b.Request(ctx, "request/topic", request, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil {
		t.Fatal("expected reply")
	}
	if string(reply.Payload) != "REPLY:PING" {
		t.Errorf("reply = %q, want %q", reply.Payload, "REPLY:PING")
	}
}

func TestDevBrokerConcurrentPublish(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, _ := b.CreateQueue("concurrent-q", QueueOptions{})
	must(t, b.Subscribe(q, "concurrent/>"))

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()
			_ = b.Publish(ctx, "concurrent/test", &Message{Payload: []byte("msg")})
		}()
	}
	wg.Wait()

	count := 0
	for {
		timeoutCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		msg, _ := b.Receive(timeoutCtx, q)
		cancel()
		if msg == nil {
			break
		}
		count++
	}

	if count != n {
		t.Errorf("received %d messages, want %d", count, n)
	}
}

func TestDevBrokerDeleteQueue(t *testing.T) {
	b := newConnectedBroker(t)

	q, _ := b.CreateQueue("del-q", QueueOptions{})
	must(t, b.Subscribe(q, "test/>"))

	must(t, b.DeleteQueue("del-q"))

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := b.Receive(timeoutCtx, q)
	if err == nil {
		t.Error("should error on deleted queue")
	}
}

func TestDevBrokerTemporaryQueue(t *testing.T) {
	t.Parallel()
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, err := b.CreateTemporaryQueue()
	if err != nil {
		t.Fatal(err)
	}
	if q.Name() == "" {
		t.Error("expected non-empty queue name")
	}

	must(t, b.Subscribe(q, "temp/test"))
	must(t, b.PublishGuaranteed(ctx, "temp/test", &Message{Payload: []byte("hello")}))

	ctx2, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(ctx2, q)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Payload) != "hello" {
		t.Errorf("payload = %q, want %q", msg.Payload, "hello")
	}
}

func TestDevBrokerTemporaryQueue_ReplyBeforeReceive(t *testing.T) {
	t.Parallel()
	b := newConnectedBroker(t)
	ctx := context.Background()

	q, err := b.CreateTemporaryQueue()
	if err != nil {
		t.Fatal(err)
	}

	replyTopic := "temp/reply/race-test"
	must(t, b.Subscribe(q, replyTopic))

	must(t, b.PublishGuaranteed(ctx, replyTopic, &Message{Payload: []byte("reply")}))

	ctx2, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	msg, err := b.Receive(ctx2, q)
	if err != nil {
		t.Fatalf("reply lost — Receive returned error: %v", err)
	}
	if msg == nil {
		t.Fatal("reply lost — Receive returned nil message")
	}
	if string(msg.Payload) != "reply" {
		t.Errorf("payload = %q, want %q", msg.Payload, "reply")
	}
}

func TestDevBrokerRequestReplyInbox(t *testing.T) {
	t.Parallel()
	b := newConnectedBroker(t)
	ctx := context.Background()

	respQ, err := b.CreateQueue("inbox-responder", QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	must(t, b.Subscribe(respQ, "inbox/request"))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			rctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			req, recvErr := b.Receive(rctx, respQ)
			cancel()
			if recvErr != nil || req == nil {
				select {
				case <-stop:
					return
				default:
					continue
				}
			}
			_ = b.Publish(ctx, req.ReplyTo, &Message{
				Payload:       req.Payload,
				CorrelationID: req.CorrelationID,
			})
		}
	}()
	t.Cleanup(func() { close(stop); wg.Wait(); _ = b.DeleteQueue(respQ.Name()) })

	reply, err := b.RequestReplyInbox(ctx, "inbox/request", &Message{Payload: []byte("ping")}, 2*time.Second)
	if err != nil {
		t.Fatalf("RequestReplyInbox: %v", err)
	}
	if string(reply.Payload) != "ping" {
		t.Errorf("reply payload = %q, want %q", reply.Payload, "ping")
	}
}

func TestDevBrokerNamedQueueCompetingConsumers(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q1, _ := b.CreateQueue("shared-q", QueueOptions{Named: true})
	q2, _ := b.CreateQueue("shared-q", QueueOptions{Named: true})
	must(t, b.Subscribe(q1, "test/competing"))

	const messageCount = 10
	for i := range messageCount {
		msg := &Message{Payload: []byte(fmt.Sprintf("msg-%d", i))}
		must(t, b.Publish(ctx, "test/competing", msg))
	}

	var mu sync.Mutex
	var r1Count, r2Count int
	var wg sync.WaitGroup
	wg.Add(2)

	drain := func(q Queue, count *int) {
		defer wg.Done()
		for {
			rctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			msg, err := b.Receive(rctx, q)
			cancel()
			if err != nil || msg == nil {
				return
			}
			mu.Lock()
			*count++
			mu.Unlock()
		}
	}
	go drain(q1, &r1Count)
	go drain(q2, &r2Count)
	wg.Wait()

	total := r1Count + r2Count
	if total != messageCount {
		t.Errorf("total messages = %d, want %d (r1=%d, r2=%d)", total, messageCount, r1Count, r2Count)
	}
}

func TestDevBrokerDifferentNamedQueuesFanout(t *testing.T) {
	b := newConnectedBroker(t)
	ctx := context.Background()

	q1, _ := b.CreateQueue("queue-a", QueueOptions{Named: true})
	q2, _ := b.CreateQueue("queue-b", QueueOptions{Named: true})
	must(t, b.Subscribe(q1, "test/fanout"))
	must(t, b.Subscribe(q2, "test/fanout"))

	msg := &Message{Payload: []byte("hello")}
	must(t, b.Publish(ctx, "test/fanout", msg))

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Second)
	m1, err := b.Receive(timeoutCtx, q1)
	cancel()
	if err != nil || m1 == nil {
		t.Fatal("queue-a should receive the message")
	}

	timeoutCtx2, cancel2 := context.WithTimeout(ctx, time.Second)
	m2, err := b.Receive(timeoutCtx2, q2)
	cancel2()
	if err != nil || m2 == nil {
		t.Fatal("queue-b should also receive the message")
	}
}
