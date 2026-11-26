// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const requeueQueueName = "requeue/q"

func requeueFixture(t *testing.T, opts QueueOptions, queueSize int) (*DevBroker, Queue) {
	t.Helper()
	b := newTestDevBroker(t, queueSize, NoopMetricsRecorder{})
	opts.Named = true
	q, err := b.CreateQueue(requeueQueueName, opts)
	must(t, err)
	must(t, b.Subscribe(q, "requeue/>"))
	return b, q
}

func publishedTask() *Message {
	return &Message{
		ID:             "message-1",
		Payload:        []byte("payload"),
		UserProperties: map[string]string{"userId": "alice@example.com"},
		ReplyTo:        "reply/topic",
		CorrelationID:  "correlation-1",
	}
}

func receiveRequeued(t *testing.T, b *DevBroker, q Queue, wait time.Duration) (*Message, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	return b.Receive(ctx, q)
}

func mustReceive(t *testing.T, b *DevBroker, q Queue) *Message {
	t.Helper()
	m, err := receiveRequeued(t, b, q, time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return m
}

func publishTask(t *testing.T, b *DevBroker) {
	t.Helper()
	must(t, b.Publish(t.Context(), "requeue/task", publishedTask()))
}

func TestDevBrokerRequeue(t *testing.T) {
	t.Run("queues a redelivered clone that keeps the message identity and does not alias the caller's copy", func(t *testing.T) {
		b, q := requeueFixture(t, QueueOptions{}, 8)
		publishTask(t, b)
		original := mustReceive(t, b, q)
		if original.Redelivered {
			t.Fatal("the first delivery was marked redelivered")
		}

		must(t, b.Requeue(requeueQueueName, original))
		original.Payload[0] = 'X'
		original.UserProperties["userId"] = "mallory@example.com"
		original.UserProperties["extra"] = "value"

		again := mustReceive(t, b, q)
		if !again.Redelivered {
			t.Error("the requeued message was not marked redelivered")
		}
		if again.Topic != "requeue/task" || again.ID != "message-1" || again.ReplyTo != "reply/topic" || again.CorrelationID != "correlation-1" {
			t.Errorf("identity not preserved: topic %q id %q replyTo %q correlationID %q", again.Topic, again.ID, again.ReplyTo, again.CorrelationID)
		}
		if string(again.Payload) != "payload" {
			t.Errorf("payload %q was changed through the caller's copy", again.Payload)
		}
		if len(again.UserProperties) != 1 || again.UserProperties["userId"] != "alice@example.com" {
			t.Errorf("user properties %v were changed through the caller's copy", again.UserProperties)
		}
	})

	t.Run("reports an unknown queue as not found", func(t *testing.T) {
		b, _ := requeueFixture(t, QueueOptions{}, 8)
		err := b.Requeue("no/such/queue", publishedTask())
		if !errors.Is(err, ErrQueueNotFound) {
			t.Fatalf("got %v, want ErrQueueNotFound", err)
		}
	})

	t.Run("fails after the send timeout when the queue is full", func(t *testing.T) {
		prev := DirectSendTimeout
		DirectSendTimeout = 20 * time.Millisecond
		t.Cleanup(func() { DirectSendTimeout = prev })
		b, _ := requeueFixture(t, QueueOptions{}, 1)
		must(t, b.Requeue(requeueQueueName, publishedTask()))

		start := time.Now()
		err := b.Requeue(requeueQueueName, publishedTask())
		if err == nil {
			t.Fatal("a requeue onto a full queue succeeded")
		}
		if errors.Is(err, ErrQueueNotFound) || errors.Is(err, ErrRedeliveryExhausted) {
			t.Fatalf("got %v, want a queue-full error", err)
		}
		if elapsed := time.Since(start); elapsed < DirectSendTimeout {
			t.Fatalf("gave up after %s, before the %s send timeout", elapsed, DirectSendTimeout)
		}
	})

	t.Run("stops requeueing once the queue's redelivery limit is used up", func(t *testing.T) {
		const limit = 2
		b, q := requeueFixture(t, QueueOptions{MaxRedelivery: limit}, 8)
		publishTask(t, b)
		current := mustReceive(t, b, q)
		for range limit {
			must(t, b.Requeue(requeueQueueName, current))
			current = mustReceive(t, b, q)
		}

		err := b.Requeue(requeueQueueName, current)
		if !errors.Is(err, ErrRedeliveryExhausted) {
			t.Fatalf("got %v, want ErrRedeliveryExhausted", err)
		}
		if m, err := receiveRequeued(t, b, q, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("an exhausted requeue still enqueued %v (err %v)", m, err)
		}
	})

	t.Run("counts redeliveries per message", func(t *testing.T) {
		b, q := requeueFixture(t, QueueOptions{MaxRedelivery: 1}, 8)
		publishTask(t, b)
		used := mustReceive(t, b, q)
		must(t, b.Requeue(requeueQueueName, used))
		used = mustReceive(t, b, q)
		publishTask(t, b)
		fresh := mustReceive(t, b, q)

		if err := b.Requeue(requeueQueueName, used); !errors.Is(err, ErrRedeliveryExhausted) {
			t.Fatalf("got %v for the message at its limit, want ErrRedeliveryExhausted", err)
		}
		must(t, b.Requeue(requeueQueueName, fresh))
	})

	t.Run("requeues without limit when the queue sets no redelivery limit", func(t *testing.T) {
		const rounds = 50
		b, q := requeueFixture(t, QueueOptions{MaxRedelivery: 0}, 8)
		publishTask(t, b)
		current := mustReceive(t, b, q)
		for range rounds {
			must(t, b.Requeue(requeueQueueName, current))
			current = mustReceive(t, b, q)
		}
	})

	t.Run("does not panic when the queue is deleted while requeues are in flight", func(t *testing.T) {
		b, _ := requeueFixture(t, QueueOptions{}, 1024)
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 200 {
					err := b.Requeue(requeueQueueName, publishedTask())
					if err != nil && !errors.Is(err, ErrQueueNotFound) {
						t.Errorf("requeue: %v", err)
						return
					}
				}
			}()
		}
		must(t, b.DeleteQueue(requeueQueueName))
		wg.Wait()
		if len(b.MatchingQueues("requeue/task")) != 0 {
			t.Fatal("the deleted queue still matches its topic")
		}
	})
}
