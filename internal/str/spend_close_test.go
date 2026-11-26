// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type heldSpendBroker struct {
	broker.Broker
	release   chan struct{}
	published atomic.Int32
}

func (b *heldSpendBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if topic != a2a.SpendEventTopic("test-ns") {
		return b.Broker.PublishGuaranteed(ctx, topic, msg)
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.published.Add(1)
	return nil
}

func TestStop_PublishesQueuedSpendAfterTheWaitUsedTheBudget(t *testing.T) {
	brk := &heldSpendBroker{Broker: newTestBroker(t), release: make(chan struct{})}
	w := newInProcessWorker(t, brk)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.spendPublisher().Record(spend.Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "user-1",
		TriggerKind: spend.TriggerChat, Surface: spend.SurfaceAgent, ModelName: "m", TaskID: "task-1", TraceID: "trace-1",
	})
	spent, cancel := context.WithCancel(t.Context())
	cancel()
	time.AfterFunc(time.Second, func() { close(brk.release) })

	if err := w.Stop(spent); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if brk.published.Load() != 1 {
		t.Errorf("spend events published at stop = %d, want the queued one", brk.published.Load())
	}
}
