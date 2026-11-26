// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"log/slog"
	"testing"
	"time"
)

func TestNoopMetricsRecorder_DoesNotPanic(t *testing.T) {
	noop := NoopMetricsRecorder{}
	noop.Counter("any", 1, "queue", "x")
	noop.Gauge("any", 1.0, "queue", "x")

	var r MetricsRecorder = noop
	r.Counter("any", 1, "queue", "x")
	r.Gauge("any", 1.0, "queue", "x")
}

func TestMetricsRecorderOrNoop(t *testing.T) {
	if r := MetricsRecorderOrNoop(nil); r == nil {
		t.Fatal("nil input must return a non-nil recorder")
	} else if _, ok := r.(NoopMetricsRecorder); !ok {
		t.Fatalf("nil input must return NoopMetricsRecorder, got %T", r)
	}

	cm := newCountingMetrics()
	if r := MetricsRecorderOrNoop(cm); r != cm {
		t.Fatal("non-nil input must be returned as-is")
	}
}

func TestSolaceBroker_SetMetrics(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, slog.Default())
	if got := *b.metrics.Load(); got == nil {
		t.Fatal("constructor must install non-nil noop metrics")
	}

	cm := newCountingMetrics()
	b.SetMetrics(cm)
	if got := *b.metrics.Load(); got != cm {
		t.Fatalf("SetMetrics(cm) did not install cm; got %T", got)
	}

	b.SetMetrics(nil)
	if _, ok := (*b.metrics.Load()).(NoopMetricsRecorder); !ok {
		t.Fatalf("SetMetrics(nil) must install NoopMetricsRecorder; got %T", *b.metrics.Load())
	}
}

func TestSolaceBroker_DeliverDirect_Success(t *testing.T) {
	b := NewSolaceBroker(SolaceBrokerConfig{}, slog.Default())
	cm := newCountingMetrics()
	b.SetMetrics(cm)

	sr := &solaceReceiver{msgChan: make(chan *Message, 4)}
	msg := &Message{ID: "x", Topic: "t", Payload: []byte("p")}

	b.deliverDirect(sr, "test-queue", msg)

	select {
	case got := <-sr.msgChan:
		if got != msg {
			t.Fatalf("wrong message: %#v", got)
		}
	default:
		t.Fatal("expected message in channel")
	}
	if drops := cm.counter("broker.direct.dropped"); drops != 0 {
		t.Fatalf("expected 0 drops, got %d", drops)
	}
}

func TestSolaceBroker_DeliverDirect_TimeoutDrops(t *testing.T) {
	prev := DirectSendTimeout
	DirectSendTimeout = 5 * time.Millisecond
	t.Cleanup(func() { DirectSendTimeout = prev })

	b := NewSolaceBroker(SolaceBrokerConfig{}, slog.Default())
	cm := newCountingMetrics()
	b.SetMetrics(cm)

	sr := &solaceReceiver{msgChan: make(chan *Message, 1)}
	sr.msgChan <- &Message{ID: "preload"}

	start := time.Now()
	b.deliverDirect(sr, "test-queue", &Message{ID: "drop-me"})
	elapsed := time.Since(start)

	if elapsed < DirectSendTimeout {
		t.Fatalf("expected to block at least %s, blocked %s", DirectSendTimeout, elapsed)
	}
	if drops := cm.counter("broker.direct.dropped"); drops != 1 {
		t.Fatalf("expected 1 drop, got %d", drops)
	}
}

func TestDevBroker_SetMetrics(t *testing.T) {
	b := NewDevBroker()
	if got := *b.metrics.Load(); got == nil {
		t.Fatal("constructor must install non-nil noop metrics")
	}
	cm := newCountingMetrics()
	b.SetMetrics(cm)
	if got := *b.metrics.Load(); got != cm {
		t.Fatalf("SetMetrics(cm) did not install cm; got %T", got)
	}
	b.SetMetrics(nil)
	if _, ok := (*b.metrics.Load()).(NoopMetricsRecorder); !ok {
		t.Fatalf("SetMetrics(nil) must install NoopMetricsRecorder; got %T", *b.metrics.Load())
	}
}

func TestNetworkDevBroker_SetMetrics(t *testing.T) {
	c := NewNetworkDevBroker("127.0.0.1:0", "id", "q", slog.Default())
	if got := *c.metrics.Load(); got == nil {
		t.Fatal("constructor must install non-nil noop metrics")
	}
	cm := newCountingMetrics()
	c.SetMetrics(cm)
	if got := *c.metrics.Load(); got != cm {
		t.Fatalf("SetMetrics(cm) did not install cm; got %T", got)
	}
	c.SetMetrics(nil)
	if _, ok := (*c.metrics.Load()).(NoopMetricsRecorder); !ok {
		t.Fatalf("SetMetrics(nil) must install NoopMetricsRecorder; got %T", *c.metrics.Load())
	}
}

func TestNetworkDevBroker_DispatchMessage_MatchingQueueSuccess(t *testing.T) {
	c := NewNetworkDevBroker("127.0.0.1:0", "id", "q", slog.Default())
	cm := newCountingMetrics()
	c.SetMetrics(cm)
	q := newNetQueueForTest("named", "evt/>", 4)
	c.queues["named"] = q

	c.dispatchMessage(&NetworkMessage{MessageID: "m1", Topic: "evt/test", Payload: "x"})

	select {
	case got := <-q.msgCh:
		if got.Topic != "evt/test" {
			t.Fatalf("unexpected topic: %q", got.Topic)
		}
	default:
		t.Fatal("expected message in queue")
	}
}

func TestNetworkDevBroker_DispatchMessage_FullQueueDrops(t *testing.T) {
	prev := DirectSendTimeout
	DirectSendTimeout = 5 * time.Millisecond
	t.Cleanup(func() { DirectSendTimeout = prev })

	c := NewNetworkDevBroker("127.0.0.1:0", "id", "q", slog.Default())
	cm := newCountingMetrics()
	c.SetMetrics(cm)
	q := newNetQueueForTest("named", "evt/>", 1)
	q.msgCh <- &Message{ID: "preload"}
	c.queues["named"] = q

	c.dispatchMessage(&NetworkMessage{MessageID: "drop-me", Topic: "evt/test", Payload: "x"})

	if drops := cm.counter("broker.direct.dropped"); drops != 1 {
		t.Fatalf("expected 1 drop, got %d", drops)
	}
}

func TestNetworkDevBroker_DispatchMessage_DefaultQueueRoutes(t *testing.T) {
	c := NewNetworkDevBroker("127.0.0.1:0", "id", "q", slog.Default())
	cm := newCountingMetrics()
	c.SetMetrics(cm)
	c.defaultQueue = newNetQueueForTest("__default__", "evt/>", 4)

	c.dispatchMessage(&NetworkMessage{MessageID: "m1", Topic: "evt/test", Payload: "x"})

	select {
	case got := <-c.defaultQueue.msgCh:
		if got.Topic != "evt/test" {
			t.Fatalf("unexpected topic: %q", got.Topic)
		}
	default:
		t.Fatal("expected message in default queue")
	}
}

func TestNetworkDevBroker_DispatchMessage_DefaultQueueFullDrops(t *testing.T) {
	prev := DirectSendTimeout
	DirectSendTimeout = 5 * time.Millisecond
	t.Cleanup(func() { DirectSendTimeout = prev })

	c := NewNetworkDevBroker("127.0.0.1:0", "id", "q", slog.Default())
	cm := newCountingMetrics()
	c.SetMetrics(cm)
	c.defaultQueue = newNetQueueForTest("__default__", "evt/>", 1)
	c.defaultQueue.msgCh <- &Message{ID: "preload"}

	c.dispatchMessage(&NetworkMessage{MessageID: "drop-me", Topic: "evt/test", Payload: "x"})

	if drops := cm.counter("broker.direct.dropped"); drops != 1 {
		t.Fatalf("expected 1 drop on default queue, got %d", drops)
	}
}
