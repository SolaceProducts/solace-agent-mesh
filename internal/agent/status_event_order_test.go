// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type delayingBroker struct {
	broker.Broker
	delaySeq1 time.Duration
}

func (d *delayingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if d.delaySeq1 > 0 && msg != nil && msg.UserProperties[a2a.PropEventSeq] == "1" {
		time.Sleep(d.delaySeq1)
	}
	return d.Broker.PublishGuaranteed(ctx, topic, msg)
}

func TestStatusPublisher_ConcurrentPublishes_PreserveSeqOrderOnWire(t *testing.T) {
	base := broker.NewDevBroker()
	if err := base.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = base.Disconnect(t.Context()) }()

	brk := &delayingBroker{Broker: base, delaySeq1: 40 * time.Millisecond}

	topic := "test-ns/a2a/v1/gateway/status/test-gw/task-order-1"
	q, err := base.CreateQueue("status-collector-order", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := base.Subscribe(q, topic); err != nil {
		t.Fatal(err)
	}

	sp := NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: topic,
		TaskID:      "task-order-1",
		ContextID:   "ctx",
		AgentName:   "agent-under-test",
		SeqGen:      NewEventSeqGen(),
	}, slog.Default())

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			sp.PublishText(t.Context(), "chunk")
		})
	}
	wg.Wait()

	msgs := receiveAll(t, base, q, 2, 3*time.Second)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	for i, msg := range msgs {
		seq, err := strconv.ParseUint(msg.UserProperties[a2a.PropEventSeq], 10, 64)
		if err != nil {
			t.Fatalf("msg %d eventSeq parse: %v (raw=%q)", i, err, msg.UserProperties[a2a.PropEventSeq])
		}
		want := uint64(i + 1)
		if seq != want {
			t.Errorf("wire position %d carried eventSeq %d, want %d — status events left the publisher out of seq order", i, seq, want)
		}
	}
}
