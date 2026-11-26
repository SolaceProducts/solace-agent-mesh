// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type streamAdapter struct {
	recordingAdapter
	mu      sync.Mutex
	records []string
}

func (a *streamAdapter) OnTaskStreamRecord(_ context.Context, taskID string, rec StreamRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, taskID+" "+rec.EventType+" "+string(rec.Data))
}

func (a *streamAdapter) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.records...)
}

var _ TaskStreamHandler = (*streamAdapter)(nil)

func newCoreOn(t *testing.T, brk broker.Broker, adapter GatewayAdapter) *GatewayCore {
	t.Helper()
	core := NewGatewayCore(adapter, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	return core
}

func newDevBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func startStreamLoops(t *testing.T, core *GatewayCore) {
	t.Helper()
	if err := core.setupSubscriptions(core.svc.Broker()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { core.taskStreamLoop(ctx) })
	wg.Go(func() { core.taskStreamPublishLoop(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
}

func TestSetupSubscriptions_StreamTopicOnlyForAStreamAdapter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		adapter GatewayAdapter
		want    bool
	}{
		{"stream adapter", &streamAdapter{}, true},
		{"other adapter", &recordingAdapter{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb := &subscribingBroker{Broker: newDevBroker(t)}
			core := newCoreOn(t, sb, tc.adapter)
			if err := core.setupSubscriptions(sb); err != nil {
				t.Fatal(err)
			}
			if got := (core.streamQueue != nil); got != tc.want {
				t.Fatalf("stream queue created = %v, want %v", got, tc.want)
			}
			if tc.want {
				topics := sb.subs[core.streamQueue.Name()]
				if len(topics) != 1 || topics[0] != a2a.GatewayStreamTaskSubscription("test-ns", "test-gw") {
					t.Fatalf("stream queue subscriptions = %v", topics)
				}
			}
		})
	}
}

func TestTaskStream_RecordReachesTheOtherInstance(t *testing.T) {
	brk := newDevBroker(t)
	pubAdapter, subAdapter := &streamAdapter{}, &streamAdapter{}
	pub, sub := newCoreOn(t, brk, pubAdapter), newCoreOn(t, brk, subAdapter)
	startStreamLoops(t, pub)
	startStreamLoops(t, sub)

	pub.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 4, EventType: "status_update", Data: []byte(`{"n":4}`)})
	pub.PublishTaskStreamRecord(testTaskID, StreamRecord{EventType: "activity_event", Data: []byte(`{"a":1}`)})

	waitUntil(t, "the other instance never received both records", func() bool { return len(subAdapter.seen()) == 2 })
	got := subAdapter.seen()
	if got[0] != testTaskID+` status_update {"n":4}` || got[1] != testTaskID+` activity_event {"a":1}` {
		t.Fatalf("records = %v", got)
	}
	if own := pubAdapter.seen(); len(own) != 0 {
		t.Fatalf("the publisher received its own records: %v", own)
	}
}

func TestHandleTaskStreamRecord_DropsWhatItCannotUse(t *testing.T) {
	adapter := &streamAdapter{}
	core := newCoreOn(t, newDevBroker(t), adapter)
	topic := a2a.GatewayStreamTaskTopic("test-ns", "test-gw", testTaskID)

	core.handleTaskStreamRecord(t.Context(), &broker.Message{Topic: topic, Payload: []byte(`not json`)})
	if got := adapter.seen(); len(got) != 0 {
		t.Fatalf("a record with an invalid body reached the adapter: %v", got)
	}

	own := &broker.Message{Topic: topic, Payload: []byte(`{"sequence":1,"eventType":"status_update"}`),
		UserProperties: map[string]string{streamPublisherProperty: core.instanceID}}
	core.handleTaskStreamRecord(t.Context(), own)
	if got := adapter.seen(); len(got) != 0 {
		t.Fatalf("the instance's own record reached its adapter: %v", got)
	}
}

func TestPublishTaskStreamRecord_NeverBlocks(t *testing.T) {
	core := newCoreOn(t, newDevBroker(t), &streamAdapter{})
	for range streamOutCap {
		core.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 1, EventType: "status_update", Data: []byte(`{}`)})
	}
	if core.streamDrops.Load() != 0 {
		t.Fatalf("dropped = %d before the queue was full", core.streamDrops.Load())
	}
	core.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 2, EventType: "status_update", Data: []byte(`{}`)})
	if core.streamDrops.Load() != 1 {
		t.Fatalf("dropped = %d, want 1 once the queue was full", core.streamDrops.Load())
	}

	plain := newCoreOn(t, newDevBroker(t), &recordingAdapter{})
	plain.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 1})
	if plain.streamRows != nil || plain.streamFrames != nil || plain.streamDrops.Load() != 0 {
		t.Fatal("an instance with no stream adapter queued a record")
	}

	frames := len(core.streamFrames)
	core.PublishTaskStreamRecord(testTaskID, StreamRecord{EventType: "activity_event", Data: []byte(`not json`)})
	if len(core.streamFrames) != frames || core.streamDrops.Load() != 1 {
		t.Fatal("a record whose body cannot be encoded was queued or counted as dropped")
	}
}

type refusingBroker struct {
	broker.Broker
	refuse  string
	mu      sync.Mutex
	deleted []string
}

func (b *refusingBroker) Subscribe(q broker.Queue, topic string) error {
	if topic == b.refuse {
		return errors.New("subscription denied by the client profile")
	}
	return b.Broker.Subscribe(q, topic)
}

func (b *refusingBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	b.deleted = append(b.deleted, name)
	b.mu.Unlock()
	return b.Broker.DeleteQueue(name)
}

func TestSetupSubscriptions_RefusedStreamSubscriptionIsNotFatal(t *testing.T) {
	rb := &refusingBroker{Broker: newDevBroker(t), refuse: a2a.GatewayStreamTaskSubscription("test-ns", "test-gw")}
	core := newCoreOn(t, rb, &streamAdapter{})
	if err := core.setupSubscriptions(rb); err != nil {
		t.Fatalf("setupSubscriptions = %v, want the instance to start without the stream subscription", err)
	}
	if core.streamQueue != nil {
		t.Fatal("the refused subscription was kept")
	}
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if len(rb.deleted) != 1 {
		t.Fatalf("deleted queues = %v, want the refused receiver", rb.deleted)
	}
	if core.streamRows == nil {
		t.Fatal("the instance no longer publishes its own records")
	}
}

type failingPublishBroker struct {
	broker.Broker
	mu    sync.Mutex
	calls int
}

func (b *failingPublishBroker) Publish(context.Context, string, *broker.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	return errors.New("broker down")
}

func TestTaskStreamPublishLoop_SurvivesAFailedPublish(t *testing.T) {
	fb := &failingPublishBroker{Broker: newDevBroker(t)}
	core := newCoreOn(t, fb, &streamAdapter{})
	var logs bytes.Buffer
	core.logger = slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); core.taskStreamPublishLoop(ctx) }()

	core.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 1, EventType: "status_update", Data: []byte(`{}`)})
	core.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 2, EventType: "status_update", Data: []byte(`{}`)})
	waitUntil(t, "the loop stopped after a failed publish", func() bool {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		return fb.calls == 2
	})
	cancel()
	<-done
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 || !strings.Contains(logs.String(), "taskID="+testTaskID) {
		t.Fatalf("want one rate-limited Warn naming the task, got:\n%s", logs.String())
	}
}

type orderBroker struct {
	broker.Broker
	mu       sync.Mutex
	payloads []string
}

func (b *orderBroker) Publish(_ context.Context, _ string, msg *broker.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.payloads = append(b.payloads, string(msg.Payload))
	return nil
}

func (b *orderBroker) published() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.payloads...)
}

func TestTaskStream_RowsGoFirstAndAreNotCrowdedOut(t *testing.T) {
	ob := &orderBroker{Broker: newDevBroker(t)}
	core := newCoreOn(t, ob, &streamAdapter{})
	for range streamOutCap + 1 {
		core.PublishTaskStreamRecord(testTaskID, StreamRecord{EventType: "activity_event", Data: []byte(`{}`)})
	}
	if core.streamDrops.Load() != 1 {
		t.Fatalf("dropped = %d, want only the frame past the frames queue", core.streamDrops.Load())
	}
	core.PublishTaskStreamRecord(testTaskID, StreamRecord{Sequence: 9, EventType: "final_response"})
	if core.streamDrops.Load() != 1 {
		t.Fatal("a row announcement was dropped behind a full frames queue")
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); core.taskStreamPublishLoop(ctx) }()
	waitUntil(t, "nothing was published", func() bool { return len(ob.published()) > 0 })
	cancel()
	<-done
	if first := ob.published()[0]; !strings.Contains(first, `"sequence":9`) {
		t.Fatalf("first published = %s, want the row announcement", first)
	}
}

func TestSetupSubscriptions_RefusedOnRestartKeepsNoReceiver(t *testing.T) {
	rb := &refusingBroker{Broker: newDevBroker(t)}
	core := newCoreOn(t, rb, &streamAdapter{})
	if err := core.setupSubscriptions(rb); err != nil {
		t.Fatal(err)
	}
	if core.streamQueue == nil {
		t.Fatal("the first start did not subscribe")
	}
	core.cleanupQueues()
	rb.refuse = a2a.GatewayStreamTaskSubscription("test-ns", "test-gw")
	if err := core.setupSubscriptions(rb); err != nil {
		t.Fatal(err)
	}
	if core.streamQueue != nil {
		t.Fatal("the restart kept the deleted receiver")
	}
}
