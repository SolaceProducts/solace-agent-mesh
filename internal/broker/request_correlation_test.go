// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newCorrTestBroker(t *testing.T) *DevBroker {
	t.Helper()
	b := NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = b.Disconnect(context.Background()) })
	return b
}

func TestRequestGeneratesCorrelationIDWhenEmpty(t *testing.T) {
	b := newCorrTestBroker(t)
	const reqTopic, replyTopic = "corr/req", "corr/reply"

	q, err := b.CreateQueue("responder", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = b.Subscribe(q, reqTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	gotCorr := make(chan string, 1)
	go func() {
		msg, recvErr := b.Receive(t.Context(), q)
		if recvErr != nil || msg == nil {
			return
		}
		gotCorr <- msg.CorrelationID
		_ = b.Publish(t.Context(), msg.ReplyTo, &Message{Payload: []byte("ok"), CorrelationID: msg.CorrelationID})
	}()
	time.Sleep(20 * time.Millisecond)

	reply, err := b.Request(t.Context(), reqTopic, &Message{Payload: []byte("hi"), ReplyTo: replyTopic}, time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	corr := <-gotCorr
	if corr == "" {
		t.Fatal("Request did not generate a CorrelationID for an empty-corr message")
	}
	if reply.CorrelationID != corr {
		t.Errorf("reply CorrelationID = %q, want %q", reply.CorrelationID, corr)
	}
}

func TestRequestConcurrentSharedReplyTopicReturnsOwnReply(t *testing.T) {
	b := newCorrTestBroker(t)
	const reqTopic, replyTopic, n = "shared/req", "shared/reply", 2

	q, err := b.CreateQueue("responder", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = b.Subscribe(q, reqTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var (
		mu      sync.Mutex
		pending []*Message
	)
	go func() {
		for {
			msg, recvErr := b.Receive(t.Context(), q)
			if recvErr != nil || msg == nil {
				return
			}
			mu.Lock()
			pending = append(pending, msg)
			batch := pending
			ready := len(pending) == n
			mu.Unlock()
			if !ready {
				continue
			}
			for _, req := range batch {
				_ = b.Publish(t.Context(), req.ReplyTo, &Message{Payload: req.Payload, CorrelationID: req.CorrelationID})
			}
			return
		}
	}()
	time.Sleep(20 * time.Millisecond)

	type res struct {
		sent string
		got  []byte
		err  error
	}
	results := make(chan res, n)
	for _, tag := range []string{"A", "B"} {
		go func() {
			reply, lookupErr := b.Request(t.Context(), reqTopic, &Message{Payload: []byte(tag), ReplyTo: replyTopic}, 2*time.Second)
			var got []byte
			if reply != nil {
				got = reply.Payload
			}
			results <- res{sent: tag, got: got, err: lookupErr}
		}()
	}
	for range n {
		r := <-results
		if r.err != nil {
			t.Fatalf("request %s: %v", r.sent, r.err)
		}
		if string(r.got) != r.sent {
			t.Errorf("request %s received reply %q, want %q (cross-talk on shared reply topic)", r.sent, r.got, r.sent)
		}
	}
}

func TestRequestSkipsStrayReply(t *testing.T) {
	b := newCorrTestBroker(t)
	const reqTopic, replyTopic = "stray/req", "stray/reply"

	q, err := b.CreateQueue("responder", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = b.Subscribe(q, reqTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	go func() {
		msg, recvErr := b.Receive(t.Context(), q)
		if recvErr != nil || msg == nil {
			return
		}
		_ = b.Publish(t.Context(), msg.ReplyTo, &Message{Payload: []byte("stray"), CorrelationID: "foreign-corr"})
		_ = b.Publish(t.Context(), msg.ReplyTo, &Message{Payload: []byte("mine"), CorrelationID: msg.CorrelationID})
	}()
	time.Sleep(20 * time.Millisecond)

	reply, err := b.Request(t.Context(), reqTopic, &Message{Payload: []byte("hi"), ReplyTo: replyTopic}, 2*time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if string(reply.Payload) != "mine" {
		t.Errorf("reply payload = %q, want \"mine\" (stray reply not skipped)", reply.Payload)
	}
}

func TestRequestAcceptsEmptyCorrelationReply(t *testing.T) {
	b := newCorrTestBroker(t)
	const reqTopic, replyTopic = "empty/req", "empty/reply"

	q, err := b.CreateQueue("responder", QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = b.Subscribe(q, reqTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	go func() {
		msg, recvErr := b.Receive(t.Context(), q)
		if recvErr != nil || msg == nil {
			return
		}
		_ = b.Publish(t.Context(), msg.ReplyTo, &Message{Payload: []byte("legacy-ok")})
	}()
	time.Sleep(20 * time.Millisecond)

	reply, err := b.Request(t.Context(), reqTopic, &Message{Payload: []byte("hi"), ReplyTo: replyTopic}, time.Second)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if string(reply.Payload) != "legacy-ok" {
		t.Errorf("reply payload = %q, want \"legacy-ok\" (empty-corr reply should be accepted)", reply.Payload)
	}
}
