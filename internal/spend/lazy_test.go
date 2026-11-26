// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type heldBroker struct {
	*broker.DevBroker
	release chan struct{}
}

func (b *heldBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func spendServices(brk broker.Broker) runtime.Services {
	return runtime.NewRuntime(brk, nil, runtime.WithLogger(discardLogger)).Services()
}

func spendEvent() Event {
	return Event{ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: SurfaceAgent, ModelName: "m"}
}

func TestLazyPublisher_ConcurrentFirstUsesShareOnePublisher(t *testing.T) {
	svc := spendServices(newSpendBus(t).broker)
	lazy := NewLazyPublisher(svc, testNamespace)
	t.Cleanup(func() { _ = lazy.Close(context.Background()) })

	const callers = 32
	got := make([]*Publisher, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() { got[i] = lazy.Get() })
	}
	wg.Wait()

	for i, p := range got {
		if p == nil || p != got[0] {
			t.Fatalf("caller %d got publisher %p, want the one publisher %p every caller shares", i, p, got[0])
		}
	}
}

func TestLazyPublisher_HandsOutNothingOnceClosed(t *testing.T) {
	svc := spendServices(newSpendBus(t).broker)
	lazy := NewLazyPublisher(svc, testNamespace)
	if lazy.Get() == nil {
		t.Fatal("no publisher with a broker")
	}

	if err := lazy.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if lazy.Get() != nil {
		t.Error("a closed lazy publisher handed out a publisher")
	}
	if err := lazy.Close(t.Context()); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestCloseDetached_FlushesThoughTheCallersContextIsAlreadyCancelled(t *testing.T) {
	bus := newSpendBus(t)
	held := &heldBroker{DevBroker: bus.broker, release: make(chan struct{})}
	p := NewPublisher(held, testNamespace, discardLogger)
	p.Record(spendEvent())

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	time.AfterFunc(50*time.Millisecond, func() { close(held.release) })
	if err := CloseDetached(cancelled, p); err != nil {
		t.Fatalf("close with a spent context: %v", err)
	}

	if events := bus.published(t, p); len(events) != 1 {
		t.Errorf("%d events published, want the one recorded before the close", len(events))
	}
}

func TestCloseDetached_GivesUpOnTheFlushAfterTheFlushTimeout(t *testing.T) {
	setFlushTimeoutForTest(t, 50*time.Millisecond)
	held := &heldBroker{DevBroker: newSpendBus(t).broker, release: make(chan struct{})}
	t.Cleanup(func() { close(held.release) })
	p := NewPublisher(held, testNamespace, discardLogger)
	p.Record(spendEvent())

	start := time.Now()
	err := CloseDetached(context.Background(), p)
	if err == nil {
		t.Fatal("a flush the broker never accepted reported no loss")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("close took %v, want it bounded by the flush timeout", elapsed)
	}
}
