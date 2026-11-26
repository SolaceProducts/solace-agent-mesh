// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type cooldownBroker struct {
	broker.Broker
	calls atomic.Int32
	n     int32
}

func (b *cooldownBroker) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	if b.calls.Add(1) <= b.n {
		return nil, broker.ErrProvisionCooldown
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func attachEventQueue(t *testing.T, core *GatewayCore, brk broker.Broker, name string) {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	core.eventQueue = q
}

func attachDiscoveryQueue(t *testing.T, core *GatewayCore, brk broker.Broker) {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("CreateDirectSubscription: %v", err)
	}
	core.discoveryQueue = q
}

func TestReceiveLoop_RetriesOnProvisionCooldown(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	cb := &cooldownBroker{Broker: brk, n: 3}
	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "cooldown-gw",
	})
	core.SetServices(runtime.NewRuntime(cb, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	attachEventQueue(t, core, brk, "cooldown-q")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.eventDispatchLoop(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for cb.calls.Load() <= cb.n {
		if time.Now().After(deadline) {
			t.Fatalf("loop only made %d Receive calls; it stopped retrying the cooldown", cb.calls.Load())
		}
		select {
		case <-done:
			t.Fatal("event dispatch loop EXITED on ErrProvisionCooldown — a retryable " +
				"capacity condition must never be terminal; this is the outage")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-done:
		t.Fatal("loop exited after the cooldowns cleared, want it still consuming")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("loop did not exit on context cancellation")
	}
}

func TestReceiveLoop_ExitsOnQueueNotFound(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "gone-gw",
	})
	core.SetServices(runtime.NewRuntime(&queueNotFoundBroker{Broker: brk}, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	attachEventQueue(t, core, brk, "gone-q")

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.eventDispatchLoop(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit on ErrQueueNotFound")
	}
}

func TestEventDispatchLoop_TeardownDoesNotLogError(t *testing.T) {
	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "teardown-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = logger
	attachEventQueue(t, core, brk, "teardown-q")

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.eventDispatchLoop(context.Background())
	}()

	time.Sleep(20 * time.Millisecond)
	core.teardown.Store(true)
	if err := brk.DeleteQueue(core.eventQueue.Name()); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit after its queue was deprovisioned")
	}

	if strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("expected no ERROR log for a planned teardown, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "receive loop exiting: queue removed for shutdown") {
		t.Errorf("expected the graceful-exit Info line, got: %s", buf.String())
	}
}

func TestDiscoveryLoop_RetriesOnProvisionCooldown(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	cb := &cooldownBroker{Broker: brk, n: 3}
	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "discovery-cooldown-gw",
	})
	core.SetServices(runtime.NewRuntime(cb, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	attachDiscoveryQueue(t, core, brk)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.discoveryLoop(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for cb.calls.Load() <= cb.n {
		if time.Now().After(deadline) {
			t.Fatalf("loop only made %d Receive calls; it stopped retrying the cooldown", cb.calls.Load())
		}
		select {
		case <-done:
			t.Fatal("discovery loop EXITED on ErrProvisionCooldown — a retryable " +
				"capacity condition must never be terminal; this is the outage")
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-done:
		t.Fatal("loop exited after the cooldowns cleared, want it still consuming")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("loop did not exit on context cancellation")
	}
}

func TestDiscoveryLoop_ExitsOnQueueNotFound(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "discovery-gone-gw",
	})
	core.SetServices(runtime.NewRuntime(&queueNotFoundBroker{Broker: brk}, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	attachDiscoveryQueue(t, core, brk)

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.discoveryLoop(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit on ErrQueueNotFound")
	}
}

func TestDiscoveryLoop_TeardownDoesNotLogError(t *testing.T) {
	var buf safeBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := NewGatewayCore(&recordingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "discovery-teardown-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.logger = logger
	attachDiscoveryQueue(t, core, brk)

	done := make(chan struct{})
	go func() {
		defer close(done)
		core.discoveryLoop(context.Background())
	}()

	time.Sleep(20 * time.Millisecond)
	core.teardown.Store(true)
	if err := brk.DeleteQueue(core.discoveryQueue.Name()); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit after its queue was deprovisioned")
	}

	if strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("expected no ERROR log for a planned teardown, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "receive loop exiting: queue removed for shutdown") {
		t.Errorf("expected the graceful-exit Info line, got: %s", buf.String())
	}
}
