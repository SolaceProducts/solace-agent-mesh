// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
)

type taskRelayAdapter struct {
	recordingAdapter
	held sync.Map
}

func (a *taskRelayAdapter) HoldsTask(taskID string) bool {
	_, ok := a.held.Load(taskID)
	return ok
}

var _ TaskEventRelay = (*taskRelayAdapter)(nil)

func relayStatusMessage(t *testing.T, taskID string, result any) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(a2a.NewResponse(taskID, result))
	if err != nil {
		t.Fatal(err)
	}
	return &broker.Message{Topic: a2a.GatewayStatusTopic("test-ns", "test-gw", taskID), Payload: payload}
}

func relayResponseMessage(t *testing.T, taskID string, resp any) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	return &broker.Message{Topic: a2a.GatewayResponseTopic("test-ns", "test-gw", taskID), Payload: payload}
}

func TestTaskEvents_ReachTheInstanceHoldingTheTask(t *testing.T) {
	brk := newDevBroker(t)
	activeAdapter, holderAdapter := &taskRelayAdapter{}, &taskRelayAdapter{}
	holderAdapter.held.Store(testTaskID, true)
	active, holder := newCoreOn(t, brk, activeAdapter), newCoreOn(t, brk, holderAdapter)
	startStreamLoops(t, active)
	startStreamLoops(t, holder)

	status := a2a.NewStatusUpdateEvent(testTaskID, "ctx", []a2a.Part{a2a.NewTextPart("working")}, false, nil)
	active.handleStatusEvent(t.Context(), relayStatusMessage(t, testTaskID, status))
	active.handleStatusEvent(t.Context(), relayStatusMessage(t, testTaskID, a2a.TaskArtifactUpdateEvent{
		TaskID: testTaskID, Kind: "artifact-update", Artifact: a2a.Artifact{Parts: []a2a.Part{a2a.NewTextPart("file")}},
	}))
	active.handleResponse(t.Context(), relayResponseMessage(t, testTaskID, a2a.NewResponse(testTaskID, a2a.Task{
		ID: testTaskID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	})))

	waitUntil(t, "the holding instance never got the final response", func() bool {
		holderAdapter.mu.Lock()
		defer holderAdapter.mu.Unlock()
		return len(holderAdapter.finalResponses) == 1
	})
	holderAdapter.mu.Lock()
	defer holderAdapter.mu.Unlock()
	if len(holderAdapter.statusUpdates) != 1 || holderAdapter.statusUpdates[0].Status.Message.Parts[0].Text != "working" {
		t.Fatalf("holder status updates = %+v, want the one the active instance received", holderAdapter.statusUpdates)
	}
	if len(holderAdapter.artifactUpdates) != 1 {
		t.Fatalf("holder artifact updates = %d, want 1", len(holderAdapter.artifactUpdates))
	}
	if holderAdapter.finalResponses[0].Status.State != a2a.TaskStateCompleted {
		t.Fatalf("holder final state = %q", holderAdapter.finalResponses[0].Status.State)
	}
}

func TestTaskEvents_AnErrorReachesTheInstanceHoldingTheTask(t *testing.T) {
	brk := newDevBroker(t)
	holderAdapter := &taskRelayAdapter{}
	holderAdapter.held.Store(testTaskID, true)
	active, holder := newCoreOn(t, brk, &taskRelayAdapter{}), newCoreOn(t, brk, holderAdapter)
	startStreamLoops(t, active)
	startStreamLoops(t, holder)

	active.handleResponse(t.Context(), relayResponseMessage(t, testTaskID, a2a.NewErrorResponse(testTaskID, -32000, "agent failed")))

	waitUntil(t, "the holding instance never got the error", func() bool {
		holderAdapter.mu.Lock()
		defer holderAdapter.mu.Unlock()
		return len(holderAdapter.errors) == 1 && holderAdapter.errors[0].Message == "agent failed"
	})
}

type countingPublishBroker struct {
	broker.Broker
	published atomic.Int32
}

func (b *countingPublishBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	b.published.Add(1)
	return b.Broker.Publish(ctx, topic, msg)
}

func TestTaskEvents_TheHoldingInstanceRelaysNothing(t *testing.T) {
	cb := &countingPublishBroker{Broker: newDevBroker(t)}
	adapter := &taskRelayAdapter{}
	adapter.held.Store(testTaskID, true)
	core := newCoreOn(t, cb, adapter)

	core.handleResponse(t.Context(), relayResponseMessage(t, testTaskID, a2a.NewResponse(testTaskID, a2a.Task{ID: testTaskID})))

	if n := cb.published.Load(); n != 0 {
		t.Fatalf("published %d records for a task this instance holds", n)
	}
	if len(adapter.finalResponses) != 1 {
		t.Fatalf("the adapter got %d final responses, want 1", len(adapter.finalResponses))
	}
}

func TestTaskEvents_AFailedRelayKeepsTheEvent(t *testing.T) {
	core := newCoreOn(t, &failingPublishBroker{Broker: newDevBroker(t)}, &taskRelayAdapter{})
	var acked atomic.Bool
	ctx, d := delivery.Attach(t.Context(), "", false, func() { acked.Store(true) })

	core.handleResponse(ctx, relayResponseMessage(t, testTaskID, a2a.NewResponse(testTaskID, a2a.Task{ID: testTaskID})))
	d.Release()

	if acked.Load() || !d.Kept() {
		t.Fatalf("acked = %v, kept = %v; want the event kept unacknowledged", acked.Load(), d.Kept())
	}
}

func TestSetupSubscriptions_StreamTopicForARelayingAdapter(t *testing.T) {
	sb := &subscribingBroker{Broker: newDevBroker(t)}
	core := newCoreOn(t, sb, &taskRelayAdapter{})
	if err := core.setupSubscriptions(sb); err != nil {
		t.Fatal(err)
	}
	if core.streamQueue == nil {
		t.Fatal("no task stream subscription for a relaying adapter")
	}
	if core.streamRows != nil {
		t.Fatal("a relaying adapter got the web UI's record publish queues")
	}
}

func TestDeliverRelayedEvent_IgnoresWhatItCannotUse(t *testing.T) {
	adapter := &taskRelayAdapter{}
	adapter.held.Store(testTaskID, true)
	core := newCoreOn(t, newDevBroker(t), adapter)
	for _, rec := range []StreamRecord{
		{EventType: relayedStatus, Data: []byte("not json")},
		{EventType: relayedResponse, Data: []byte(`{"jsonrpc":"2.0","id":"x","result":"not a task"}`)},
		{EventType: relayedStatus, Data: []byte(`{"jsonrpc":"2.0","id":"x","result":"not an event"}`)},
		{EventType: relayedStatus, Data: []byte(`{"jsonrpc":"2.0","id":"x","result":{"kind":"artifact-update","artifact":"not an artifact"}}`)},
	} {
		core.deliverRelayedEvent(t.Context(), adapter, testTaskID, rec)
	}
	core.deliverRelayedEvent(t.Context(), adapter, "another-task", StreamRecord{EventType: relayedStatus,
		Data: []byte(`{"jsonrpc":"2.0","id":"x","result":{"kind":"status-update","status":{"state":"working"}}}`)})

	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if n := len(adapter.statusUpdates) + len(adapter.artifactUpdates) + len(adapter.finalResponses) + len(adapter.errors); n != 0 {
		t.Errorf("adapter received %d events, want none", n)
	}
}
