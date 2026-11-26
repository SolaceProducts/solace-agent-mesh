// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type loadingAdapter struct {
	recordingAdapter
	failures atomic.Int32
	loads    atomic.Int32
}

func (a *loadingAdapter) LoadTask(context.Context, string) error {
	a.loads.Add(1)
	if a.failures.Add(-1) >= 0 {
		return errors.New("database unavailable")
	}
	return nil
}

var _ TaskStateLoader = (*loadingAdapter)(nil)

func newLoadingCore(t *testing.T, failures int32) (*GatewayCore, *loadingAdapter) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	adapter := &loadingAdapter{}
	adapter.failures.Store(failures)
	core := NewGatewayCore(adapter, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	core.logger = slog.New(slog.DiscardHandler)
	return core, adapter
}

const testTaskID = "0199b6a4-5d3e-7c1a-9f00-2b6f1c0d4e5a"

func statusMessage(t *testing.T, text string, acked *atomic.Int32) *broker.Message {
	t.Helper()
	return statusMessageFor(t, testTaskID, text, acked)
}

func statusMessageFor(t *testing.T, taskID, text string, acked *atomic.Int32) *broker.Message {
	t.Helper()
	payload := `{"jsonrpc":"2.0","id":"t","result":{"kind":"status-update","taskId":"` + taskID + `","status":{"state":"working","message":{"kind":"message","role":"agent","messageId":"m","parts":[{"kind":"text","text":"` + text + `"}]}}}}`
	return broker.NewDeliveredMessage(text, a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), []byte(payload),
		func() { acked.Add(1) }, func() { t.Error("an event was nacked") })
}

func TestEventWorker_HoldsTheTaskUntilItsStateLoads(t *testing.T) {
	core, adapter := newLoadingCore(t, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var acked atomic.Int32
	ch := make(chan *broker.Message, 2)
	ch <- statusMessage(t, "first", &acked)
	ch <- statusMessage(t, "second", &acked)
	go core.eventWorkerLoop(ctx, ch)

	deadline := time.Now().Add(10 * time.Second)
	for acked.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("events acked = %d, want 2", acked.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.statusUpdates) != 2 ||
		adapter.statusUpdates[0].Status.Message.Parts[0].Text != "first" ||
		adapter.statusUpdates[1].Status.Message.Parts[0].Text != "second" {
		t.Fatalf("status updates = %d, want first then second", len(adapter.statusUpdates))
	}
	if adapter.loads.Load() != 4 {
		t.Fatalf("LoadTask calls = %d, want two failures then one success per event", adapter.loads.Load())
	}
}

func TestEventWorker_StopsWithoutAckingAHeldEvent(t *testing.T) {
	core, adapter := newLoadingCore(t, 1000)
	ctx, cancel := context.WithCancel(t.Context())
	var acked atomic.Int32
	ch := make(chan *broker.Message, 1)
	ch <- statusMessage(t, "held", &acked)
	done := make(chan struct{})
	go func() { core.eventWorkerLoop(ctx, ch); close(done) }()

	for adapter.loads.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not stop")
	}
	if acked.Load() != 0 {
		t.Fatal("a held event was acked")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.statusUpdates) != 0 {
		t.Fatal("a held event was handed to the adapter")
	}
}

func TestEventWorker_SkipsTheLoadForANonUUIDTaskID(t *testing.T) {
	core, adapter := newLoadingCore(t, 1000)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var acked atomic.Int32
	ch := make(chan *broker.Message, 1)
	ch <- statusMessageFor(t, "gdk-task-1", "foreign", &acked)
	go core.eventWorkerLoop(ctx, ch)

	deadline := time.Now().Add(5 * time.Second)
	for acked.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the event was not acked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if adapter.loads.Load() != 0 {
		t.Fatalf("LoadTask calls = %d, want none", adapter.loads.Load())
	}
}
