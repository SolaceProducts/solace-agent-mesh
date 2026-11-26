// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingMetrics struct {
	mu       sync.Mutex
	counters map[string]int64
	gauges   map[string]float64
}

func newCountingMetrics() *countingMetrics {
	return &countingMetrics{
		counters: make(map[string]int64),
		gauges:   make(map[string]float64),
	}
}

func (c *countingMetrics) Counter(name string, delta int64, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters[c.key(name, labels)] += delta
}

func (c *countingMetrics) Gauge(name string, value float64, labels ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gauges[c.key(name, labels)] = value
}

func (c *countingMetrics) counter(name string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for k, v := range c.counters {
		if k == name || (len(k) > len(name) && k[:len(name)] == name && k[len(name)] == '|') {
			total += v
		}
	}
	return total
}

func (c *countingMetrics) key(name string, labels []string) string {
	if len(labels) == 0 {
		return name
	}
	out := name
	for _, l := range labels {
		out += "|" + l
	}
	return out
}

func newTestDevBroker(t *testing.T, queueSize int, metrics MetricsRecorder) *DevBroker {
	t.Helper()
	b := NewDevBroker()
	b.queueSize = queueSize
	b.SetMetrics(metrics)
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = b.Disconnect(context.Background()) })
	return b
}

func subscribe(t *testing.T, b *DevBroker, topic string) Queue {
	t.Helper()
	q, err := b.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create direct subscription: %v", err)
	}
	if err := b.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe %q: %v", topic, err)
	}
	return q
}

func TestDirectBackpressure_BurstAbsorption(t *testing.T) {
	const burst = 200
	metrics := newCountingMetrics()
	b := newTestDevBroker(t, burst*2, metrics)
	q := subscribe(t, b, "burst/topic")

	received := make(chan struct{}, burst)
	go func() {
		for range burst {
			msg, err := b.Receive(t.Context(), q)
			if err != nil {
				return
			}
			_ = msg
			time.Sleep(500 * time.Microsecond)
			received <- struct{}{}
		}
	}()

	for i := range burst {
		msg := &Message{ID: fmt.Sprintf("m%d", i), Payload: []byte("x")}
		if err := b.Publish(t.Context(), "burst/topic", msg); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	timeout := time.After(10 * time.Second)
	for i := range burst {
		select {
		case <-received:
		case <-timeout:
			t.Fatalf("consumer stalled after %d/%d messages", i, burst)
		}
	}
	if drops := metrics.counter("broker.direct.dropped"); drops != 0 {
		t.Fatalf("expected 0 drops, got %d", drops)
	}
}

func TestDirectBackpressure_SustainedOverloadDrops(t *testing.T) {
	prev := DirectSendTimeout
	DirectSendTimeout = 20 * time.Millisecond
	t.Cleanup(func() { DirectSendTimeout = prev })

	const (
		bufferSize = 4
		toSend     = 60
		consumeGap = 100 * time.Millisecond
	)

	metrics := newCountingMetrics()
	b := newTestDevBroker(t, bufferSize, metrics)
	q := subscribe(t, b, "overload/topic")

	var received atomic.Int64
	consumerCtx, stopConsumer := context.WithCancel(context.Background())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for {
			msg, err := b.Receive(consumerCtx, q)
			if err != nil {
				return
			}
			_ = msg
			received.Add(1)
			time.Sleep(consumeGap)
		}
	}()
	t.Cleanup(stopConsumer)

	publishDone := make(chan struct{})
	go func() {
		defer close(publishDone)
		for i := range toSend {
			msg := &Message{ID: fmt.Sprintf("m%d", i), Payload: []byte("x")}
			if err := b.Publish(context.Background(), "overload/topic", msg); err != nil {
				t.Errorf("publish %d: %v", i, err)
				return
			}
		}
	}()

	select {
	case <-publishDone:
	case <-time.After(30 * time.Second):
		t.Fatalf("publishers did not finish (suspected deadlock)")
	}

	drops := metrics.counter("broker.direct.dropped")
	if drops == 0 {
		t.Fatalf("expected drops > 0 under sustained overload, got 0 (received=%d)", received.Load())
	}
	if received.Load() == 0 {
		t.Fatalf("expected some messages to be delivered, got 0 (drops=%d)", drops)
	}

	stopConsumer()
	select {
	case <-consumerDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("consumer goroutine did not exit (suspected deadlock)")
	}
}

func TestDirectBackpressure_CrossSubscriptionIsolation(t *testing.T) {
	prev := DirectSendTimeout
	DirectSendTimeout = 20 * time.Millisecond
	t.Cleanup(func() { DirectSendTimeout = prev })

	metrics := newCountingMetrics()
	b := newTestDevBroker(t, 4, metrics)

	stalled := subscribe(t, b, "stalled/topic")
	active := subscribe(t, b, "active/topic")

	for i := range 4 {
		if err := b.Publish(t.Context(), "stalled/topic", &Message{ID: fmt.Sprintf("s%d", i), Payload: []byte("x")}); err != nil {
			t.Fatalf("prime stalled %d: %v", i, err)
		}
	}

	stopBackground := make(chan struct{})
	backgroundDone := make(chan struct{})
	go func() {
		defer close(backgroundDone)
		for i := 0; ; i++ {
			select {
			case <-stopBackground:
				return
			default:
			}
			_ = b.Publish(context.Background(), "stalled/topic",
				&Message{ID: fmt.Sprintf("sx%d", i), Payload: []byte("x")})
		}
	}()
	t.Cleanup(func() {
		close(stopBackground)
		<-backgroundDone
	})

	for i := range 5 {
		if err := b.Publish(t.Context(), "active/topic", &Message{ID: fmt.Sprintf("a%d", i), Payload: []byte("x")}); err != nil {
			t.Fatalf("publish active %d: %v", i, err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		msg, err := b.Receive(ctx, active)
		cancel()
		if err != nil {
			t.Fatalf("active receive %d: %v", i, err)
		}
		if msg.Topic != "active/topic" {
			t.Fatalf("active receive %d wrong topic: %q", i, msg.Topic)
		}
	}

	dropDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(dropDeadline) {
		if metrics.counter("broker.direct.dropped") > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if drops := metrics.counter("broker.direct.dropped"); drops == 0 {
		t.Fatalf("expected drops on stalled subscription, got 0 — buffer may not have filled")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for i := range 4 {
		if _, err := b.Receive(ctx, stalled); err != nil {
			t.Fatalf("stalled drain %d: %v", i, err)
		}
	}
}
