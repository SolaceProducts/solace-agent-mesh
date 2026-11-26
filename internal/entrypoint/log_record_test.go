// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type logAdapter struct {
	recordingAdapter
	logMu      sync.Mutex
	records    []string
	releases   []func()
	deliveries []seenDelivery
}

type seenDelivery struct {
	brokerMessageID string
	redelivered     bool
}

func (a *logAdapter) OnStatusUpdate(ctx context.Context, taskID string, evt *a2a.TaskStatusUpdateEvent, raw []byte) {
	a.recordingAdapter.OnStatusUpdate(ctx, taskID, evt, raw)
	if d := delivery.From(ctx); d != nil {
		a.logMu.Lock()
		a.releases = append(a.releases, d.Hold())
		a.deliveries = append(a.deliveries, seenDelivery{d.BrokerMessageID, d.Redelivered})
		a.logMu.Unlock()
	}
}

func (a *logAdapter) OnLogRecord(_ context.Context, taskID string, payload []byte) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	a.records = append(a.records, taskID+" "+string(payload))
}

func (a *logAdapter) held() int {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	return len(a.releases)
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var _ LogRecordHandler = (*logAdapter)(nil)

func newCoreWith(t *testing.T, adapter GatewayAdapter) (*GatewayCore, broker.Broker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := NewGatewayCore(adapter, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	return core, brk
}

func TestEventWorker_AckWaitsForTheAdaptersHold(t *testing.T) {
	adapter := &logAdapter{}
	core, _ := newCoreWith(t, adapter)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var acked atomic.Int32
	ch := make(chan *broker.Message, 2)
	ch <- statusMessage(t, "held", &acked)
	ch <- statusMessage(t, "next", &acked)
	go core.eventWorkerLoop(ctx, ch)

	waitUntil(t, "the adapter never received both events", func() bool { return adapter.held() == 2 })
	if acked.Load() != 0 {
		t.Fatal("acknowledged while the adapter held the event")
	}
	adapter.logMu.Lock()
	adapter.releases[0]()
	adapter.logMu.Unlock()
	if acked.Load() != 1 {
		t.Fatalf("acked = %d, want 1 after the hold is released", acked.Load())
	}
}

func TestEventWorker_AttachesTheBrokerMessageIDAndRedeliveredFlag(t *testing.T) {
	adapter := &logAdapter{}
	core, _ := newCoreWith(t, adapter)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var acked atomic.Int32
	msg := statusMessage(t, "again", &acked)
	msg.ReplicationGroupMessageID = "rmid1:1-2-3"
	msg.Redelivered = true
	ch := make(chan *broker.Message, 1)
	ch <- msg
	go core.eventWorkerLoop(ctx, ch)

	waitUntil(t, "the adapter never received the event", func() bool { return adapter.held() == 1 })
	adapter.logMu.Lock()
	defer adapter.logMu.Unlock()
	if got := adapter.deliveries[0]; got.brokerMessageID != "rmid1:1-2-3" || !got.redelivered {
		t.Fatalf("delivery = %+v, want the message's id and redelivered flag", got)
	}
}

func TestEventWorker_RoutesLogRecords(t *testing.T) {
	adapter := &logAdapter{}
	core, _ := newCoreWith(t, adapter)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var acked atomic.Int32
	msg := broker.NewDeliveredMessage("m", a2a.GatewayLogTopic("test-ns", "test-gw", testTaskID), []byte(`{"x":1}`),
		func() { acked.Add(1) }, func() { t.Error("nacked") })
	ch := make(chan *broker.Message, 1)
	ch <- msg
	go core.eventWorkerLoop(ctx, ch)

	waitUntil(t, "log record never acknowledged", func() bool { return acked.Load() == 1 })
	adapter.logMu.Lock()
	defer adapter.logMu.Unlock()
	if len(adapter.records) != 1 || adapter.records[0] != testTaskID+` {"x":1}` {
		t.Fatalf("records = %v", adapter.records)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.statusUpdates) != 0 {
		t.Fatal("a log record was handled as an agent status update")
	}
}

type subscribingBroker struct {
	broker.Broker
	mu   sync.Mutex
	subs map[string][]string
}

func (b *subscribingBroker) Subscribe(q broker.Queue, topic string) error {
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[string][]string{}
	}
	b.subs[q.Name()] = append(b.subs[q.Name()], topic)
	b.mu.Unlock()
	return b.Broker.Subscribe(q, topic)
}

func TestSetupSubscriptions_LogTopicOnlyForALogAdapter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapter GatewayAdapter
		want    bool
	}{
		{"log adapter", &logAdapter{}, true},
		{"other adapter", &recordingAdapter{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, brk := newCoreWith(t, tc.adapter)
			sb := &subscribingBroker{Broker: brk}
			if err := core.setupSubscriptions(sb); err != nil {
				t.Fatal(err)
			}
			got := false
			for _, topic := range sb.subs[core.eventQueue.Name()] {
				if topic == a2a.GatewayLogSubscription("test-ns", "test-gw") {
					got = true
				}
			}
			if got != tc.want {
				t.Fatalf("event queue subscribed to log records = %v, want %v (%v)", got, tc.want, sb.subs[core.eventQueue.Name()])
			}
		})
	}
}
