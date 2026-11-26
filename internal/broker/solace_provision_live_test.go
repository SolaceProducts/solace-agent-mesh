// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const provisionLiveBacklog = 200

func TestSolaceProvisionQueueWithBacklogDoesNotStallPublishes(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	ctx := context.Background()
	b := NewSolaceBroker(cfg, testLogger())
	must(t, b.Connect(ctx))
	defer func() { _ = b.Disconnect(ctx) }()

	suffix := time.Now().UnixNano()
	queue := fmt.Sprintf("sam-go/test/provision-backlog/%d", suffix)
	topic := fmt.Sprintf("sam-go/test/provision-backlog/%d/event", suffix)
	defer func() { _ = b.DeprovisionQueue(queue) }()

	opts := QueueOptions{Named: true, Exclusive: true}
	must(t, b.ProvisionQueue(ctx, queue, opts, topic))
	for i := 0; i < provisionLiveBacklog; i++ {
		must(t, b.PublishGuaranteed(ctx, topic, &Message{Payload: []byte("backlog")}))
	}

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		maxPublish time.Duration
		stop       = make(chan struct{})
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			_ = b.Publish(ctx, topic+"/other", &Message{Payload: []byte("a2a")})
			mu.Lock()
			maxPublish = max(maxPublish, time.Since(start))
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	start := time.Now()
	err := b.ProvisionQueue(ctx, queue, opts, topic)
	provisionTook := time.Since(start)
	close(stop)
	wg.Wait()

	t.Logf("second ProvisionQueue with %d-message backlog: took %v, err=%v; max concurrent Publish latency %v",
		provisionLiveBacklog, provisionTook, err, maxPublish)
	if err != nil {
		t.Errorf("ProvisionQueue with a backlog = %v, want nil", err)
	}
	if maxPublish > time.Second {
		t.Errorf("Publish stalled %v while ProvisionQueue ran, want under 1s", maxPublish)
	}
}

func TestSolaceProvisionQueueHonoursCancelledContext(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	b := NewSolaceBroker(cfg, testLogger())
	must(t, b.Connect(context.Background()))
	defer func() { _ = b.Disconnect(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	name := fmt.Sprintf("sam-go/test/provision-cancel/%d", time.Now().UnixNano())
	defer func() { _ = b.DeprovisionQueue(name) }()
	if err := b.ProvisionQueue(ctx, name, QueueOptions{Named: true}, "sam-go/test/none"); !errors.Is(err, context.Canceled) {
		t.Errorf("ProvisionQueue on a cancelled context = %v, want context.Canceled", err)
	}
}

func TestSolaceProvisionQueueReturnsAtTheContextDeadlineWithABacklog(t *testing.T) {
	cfg := solaceBrokerConfig(t)
	ctx := context.Background()
	b := NewSolaceBroker(cfg, testLogger())
	must(t, b.Connect(ctx))
	defer func() { _ = b.Disconnect(ctx) }()

	suffix := time.Now().UnixNano()
	queue := fmt.Sprintf("sam-go/test/provision-deadline/%d", suffix)
	topic := queue + "/event"
	defer func() { _ = b.DeprovisionQueue(queue) }()

	opts := QueueOptions{Named: true, Exclusive: true}
	must(t, b.ProvisionQueue(ctx, queue, opts, topic))
	for i := 0; i < provisionLiveBacklog; i++ {
		must(t, b.PublishGuaranteed(ctx, topic, &Message{Payload: []byte("backlog")}))
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = b.ProvisionQueue(deadlineCtx, queue, opts, topic)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("ProvisionQueue took %v with a 500ms deadline, want it to return near the deadline", took)
	}
}
