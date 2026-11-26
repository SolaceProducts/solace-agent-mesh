// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type provisionFailingBroker struct {
	*broker.DevBroker
	mu        sync.Mutex
	failures  int
	attempts  []time.Time
	published int
}

func (b *provisionFailingBroker) ProvisionQueue(ctx context.Context, name string, opts broker.QueueOptions, topics ...string) error {
	b.mu.Lock()
	b.attempts = append(b.attempts, time.Now())
	if b.failures > 0 {
		b.failures--
		b.mu.Unlock()
		return errors.New("semp unavailable")
	}
	b.mu.Unlock()
	return b.DevBroker.ProvisionQueue(ctx, name, opts, topics...)
}

func (b *provisionFailingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if err := b.DevBroker.PublishGuaranteed(ctx, topic, msg); err != nil {
		return err
	}
	b.mu.Lock()
	b.published++
	b.mu.Unlock()
	return nil
}

func (b *provisionFailingBroker) snapshot() (attempts []time.Time, published int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]time.Time(nil), b.attempts...), b.published
}

func TestPublisher_DroppedCountsEveryEventRecordTurnedAway(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	pub := newPublisher(dev, testNamespace, discardLogger, fastPublisherTiming)
	if err := pub.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	pub.Record(Event{ID: idgen.New()})
	pub.Record(Event{ID: idgen.New()})

	if got := pub.Dropped(); got != 2 {
		t.Errorf("dropped = %d, want both events recorded after Close", got)
	}
	if got := (*Publisher)(nil).Dropped(); got != 0 {
		t.Errorf("nil publisher dropped = %d, want 0", got)
	}
}

func TestPublisher_QueuedCountsTheEventsWaitingBehindAStalledPublish(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	held := &heldBroker{DevBroker: dev, release: make(chan struct{})}
	pub := newPublisher(held, testNamespace, discardLogger, fastPublisherTiming)

	for range 3 {
		pub.Record(spendEvent())
	}
	deadline := time.Now().Add(5 * time.Second)
	for pub.Queued() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := pub.Queued(); got != 2 {
		t.Errorf("queued = %d, want the 2 events behind the one being published", got)
	}

	close(held.release)
	if err := pub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := pub.Queued(); got != 0 {
		t.Errorf("queued after Close = %d, want 0", got)
	}
	if got := (*Publisher)(nil).Queued(); got != 0 {
		t.Errorf("nil publisher queued = %d, want 0", got)
	}
}

func TestPublisher_AFailedProvisionStillPublishesAndIsRetriedNoSoonerThanProvisionRetry(t *testing.T) {
	const retry = 60 * time.Millisecond
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	brk := &provisionFailingBroker{DevBroker: dev, failures: 2}
	timing := fastPublisherTiming
	timing.provisionRetry = retry
	pub := newPublisher(brk, testNamespace, discardLogger, timing)
	t.Cleanup(func() { _ = pub.Close(context.Background()) })

	waitPublished := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, got := brk.snapshot(); got >= want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("only %d events published, want %d", brk.published, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	record := func() {
		pub.Record(Event{
			ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: SurfaceAgent, ModelName: "m",
		})
	}

	record()
	record()
	waitPublished(2)
	if attempts, _ := brk.snapshot(); len(attempts) != 1 {
		t.Errorf("%d provision attempts for events recorded within provisionRetry, want 1", len(attempts))
	}

	time.Sleep(retry)
	record()
	waitPublished(3)
	time.Sleep(retry)
	record()
	waitPublished(4)
	time.Sleep(retry)
	record()
	waitPublished(5)

	attempts, _ := brk.snapshot()
	if len(attempts) != 3 {
		t.Fatalf("%d provision attempts, want the two failures and the success, then no more", len(attempts))
	}
	for i := 1; i < len(attempts); i++ {
		if gap := attempts[i].Sub(attempts[i-1]); gap < retry {
			t.Errorf("attempts %d and %d are %v apart, want at least provisionRetry %v", i, i+1, gap, retry)
		}
	}
}
