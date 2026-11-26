// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestSubscribe_TaskKnownOnlyFromItsRecord(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	const owner, other = "owner@example.com", "other@example.com"
	taskID := idgen.New()
	if err := core.Tasks().Register(taskID, "agent-a", owner, entrypoint.TaskIdentity{SessionID: idgen.New()}); err != nil {
		t.Fatal(err)
	}

	rec := subscribeThroughMux(t, adapter, taskID, owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "SSE reconnection established") {
		t.Fatalf("owner: status %d body %q, want a stream waiting on the database", rec.Code, rec.Body.String())
	}

	rec = subscribeThroughMux(t, adapter, taskID, other)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("another user: status %d, want 404", rec.Code)
	}

	rec = subscribeThroughMux(t, adapter, idgen.New(), owner)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: status %d, want 404", rec.Code)
	}
}

func TestOnError_TakesTheSessionFromTheTaskRecord(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	taskID, sessionID := idgen.New(), idgen.New()
	if err := core.Tasks().Register(taskID, "agent-a", "u1", entrypoint.TaskIdentity{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`{"jsonrpc":"2.0","id":"` + taskID + `","error":{"code":-32000,"message":"boom"}}`)
	adapter.OnError(t.Context(), taskID, &a2a.Error{Code: -32000, Message: "boom"}, raw)

	events, err := adapter.eventLog.Replay(t.Context(), taskID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "final_response" {
		t.Fatalf("stored %d events, want the error frame", len(events))
	}
}

func TestOnLogRecord_AppendsTheAnsweredMarker(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	taskID, sessionID := idgen.New(), idgen.New()
	if err := core.Tasks().Register(taskID, "agent-a", "u1", entrypoint.TaskIdentity{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	marker, err := answeredMarker(taskID, sessionID, "req-1", "answered")
	if err != nil {
		t.Fatal(err)
	}

	unknown := idgen.New()
	adapter.OnLogRecord(t.Context(), unknown, marker)
	if got := adapter.eventLog.FindLastEventByDataType(t.Context(), unknown, "user_input_response"); got != nil {
		t.Fatal("a marker for a task this instance does not know was stored")
	}
	adapter.OnLogRecord(t.Context(), taskID, []byte(`{"jsonrpc":"2.0","result":{"kind":"task","id":"`+taskID+`"}}`))
	if got := adapter.eventLog.FindLastEventByDataType(t.Context(), taskID, "user_input_response"); got != nil {
		t.Fatal("a record that is not a status update was stored")
	}

	adapter.OnLogRecord(t.Context(), taskID, marker)

	got := adapter.eventLog.FindLastEventByDataType(t.Context(), taskID, "user_input_response")
	if got == nil {
		t.Fatal("marker not in the event log")
	}
	if data := extractDataPartFromPayload(got, "user_input_response"); data == nil || data["request_id"] != "req-1" {
		t.Fatalf("marker data = %v", data)
	}
}

type failingRecordRegistry struct {
	entrypoint.TaskRegistry
	getFailures      *atomic.Int32
	runStateFailures *atomic.Int32
}

func (r *failingRecordRegistry) Get(taskID string) *entrypoint.TaskInfo {
	if r.getFailures.Add(-1) >= 0 {
		return nil
	}
	return r.TaskRegistry.Get(taskID)
}

func (r *failingRecordRegistry) RunState(taskID string) (running, known bool) {
	if r.runStateFailures.Add(-1) >= 0 {
		return false, false
	}
	return r.TaskRegistry.RunState(taskID)
}

func failures(n int32) *atomic.Int32 {
	c := &atomic.Int32{}
	c.Store(n)
	return c
}

func TestSubscribe_RecordReadErrorFailsClosed(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	taskID := idgen.New()
	if err := core.Tasks().Register(taskID, "agent-a", "owner@example.com", entrypoint.TaskIdentity{SessionID: idgen.New()}); err != nil {
		t.Fatal(err)
	}

	core.UseTaskRegistryForTest(&failingRecordRegistry{TaskRegistry: core.Tasks(), getFailures: failures(0), runStateFailures: failures(1000)})
	if rec := subscribeThroughMux(t, adapter, taskID, "owner@example.com"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("run state unreadable: status %d, want 503", rec.Code)
	}

	core.UseTaskRegistryForTest(&failingRecordRegistry{TaskRegistry: core.Tasks().(*failingRecordRegistry).TaskRegistry, getFailures: failures(1000), runStateFailures: failures(0)})
	if rec := subscribeThroughMux(t, adapter, taskID, "owner@example.com"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("record unreadable while running: status %d, want 503", rec.Code)
	}
}

func TestOnError_RetriesTheTaskRecordRead(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	taskID, sessionID := idgen.New(), idgen.New()
	if err := core.Tasks().Register(taskID, "agent-a", "u1", entrypoint.TaskIdentity{SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	core.UseTaskRegistryForTest(&failingRecordRegistry{TaskRegistry: core.Tasks(), getFailures: failures(1), runStateFailures: failures(0)})

	var acks atomic.Int32
	ctx, d := delivery.Attach(t.Context(), "m1", false, func() { acks.Add(1) })
	raw := []byte(`{"jsonrpc":"2.0","id":"` + taskID + `","error":{"code":-32000,"message":"boom"}}`)
	adapter.OnError(ctx, taskID, &a2a.Error{Code: -32000, Message: "boom"}, raw)
	d.Release()

	events, err := adapter.eventLog.Replay(t.Context(), taskID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "final_response" {
		t.Fatalf("stored %d events, want the error frame after the retried read", len(events))
	}
	waitFor(t, "the acknowledgement once the flusher stores the row", func() bool { return acks.Load() == 1 })
}

type deliveryRecordingLog struct {
	eventlog.TaskEventLog
	mu   sync.Mutex
	seen map[string]bool
}

func (l *deliveryRecordingLog) Append(ctx context.Context, taskID, sessionID, userID, eventType string, payload []byte) (int64, error) {
	l.mu.Lock()
	l.seen[eventType] = delivery.From(ctx) != nil
	l.mu.Unlock()
	return l.TaskEventLog.Append(ctx, taskID, sessionID, userID, eventType, payload)
}

func TestAdapterEvents_PassTheDeliveryToTheEventLog(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	log := &deliveryRecordingLog{TaskEventLog: adapter.eventLog, seen: map[string]bool{}}
	adapter.eventLog = log
	taskID, sessionID := idgen.New(), idgen.New()
	received := func() context.Context {
		ctx, _ := delivery.Attach(t.Context(), "m", false, func() {})
		return ctx
	}

	adapter.OnStatusUpdate(received(), taskID, &a2a.TaskStatusUpdateEvent{ContextID: sessionID}, []byte(`{}`))
	adapter.OnArtifactUpdate(received(), taskID, &a2a.TaskArtifactUpdateEvent{ContextID: sessionID}, []byte(`{}`))
	adapter.OnFinalResponse(received(), taskID, &a2a.Task{ID: taskID, ContextID: sessionID}, []byte(`{}`))

	log.mu.Lock()
	defer log.mu.Unlock()
	for _, eventType := range []string{"status_update", "artifact_update", "final_response"} {
		if !log.seen[eventType] {
			t.Errorf("%s reached the event log without its delivery (seen %v)", eventType, log.seen)
		}
	}
}

func TestTaskRecord_SkipsATaskIDThatIsNotAUUID(t *testing.T) {
	id := idgen.New()
	for _, taskID := range []string{"not-a-uuid", "urn:uuid:" + id, "{" + id + "}", "x" + id + "x"} {
		t.Run(taskID, func(t *testing.T) {
			adapter, core, _, _, _ := setupTestAdapterWithDB(t)
			reads := failures(1000)
			core.UseTaskRegistryForTest(&failingRecordRegistry{TaskRegistry: core.Tasks(), getFailures: reads, runStateFailures: failures(1000)})
			bounded, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			ctx, d := delivery.Attach(bounded, "m1", false, func() {})
			defer d.Release()

			if info := adapter.taskRecord(ctx, taskID); info != nil {
				t.Fatalf("taskRecord = %+v, want nil", info)
			}
			if n := reads.Load(); n != 1000 {
				t.Fatalf("task record read %d times, want none", 1000-n)
			}
		})
	}
}

type completeCountingLog struct {
	eventlog.TaskEventLog
	keep      bool
	completes atomic.Int32
}

func (l *completeCountingLog) Append(ctx context.Context, taskID, sessionID, userID, eventType string, payload []byte) (int64, error) {
	if l.keep {
		delivery.From(ctx).Keep()
		return 0, eventlog.ErrHeldForRedelivery
	}
	return l.TaskEventLog.Append(ctx, taskID, sessionID, userID, eventType, payload)
}

func (l *completeCountingLog) Complete(ctx context.Context, taskID string) error {
	l.completes.Add(1)
	return l.TaskEventLog.Complete(ctx, taskID)
}

func TestTerminalEvent_KeptForRedeliveryDoesNotCompleteTheTask(t *testing.T) {
	t.Run("final answer held by the event log", func(t *testing.T) {
		adapter, _, _, _, _ := setupTestAdapterWithDB(t)
		log := &completeCountingLog{TaskEventLog: adapter.eventLog, keep: true}
		adapter.eventLog = log
		taskID, sessionID := idgen.New(), idgen.New()
		ctx, d := delivery.Attach(t.Context(), "m-final", false, func() {})
		defer d.Release()

		adapter.OnFinalResponse(ctx, taskID, &a2a.Task{ID: taskID, ContextID: sessionID}, []byte(`{}`))
		if n := log.completes.Load(); n != 0 {
			t.Fatalf("Complete called %d times, want 0", n)
		}
	})
	t.Run("error frame held by the task record read", func(t *testing.T) {
		adapter, core, _, _, _ := setupTestAdapterWithDB(t)
		log := &completeCountingLog{TaskEventLog: adapter.eventLog}
		adapter.eventLog = log
		core.UseTaskRegistryForTest(&failingRecordRegistry{TaskRegistry: core.Tasks(), getFailures: failures(1000), runStateFailures: failures(1000)})
		stopping, cancel := context.WithCancel(t.Context())
		cancel()
		ctx, d := delivery.Attach(stopping, "m-error", false, func() {})
		defer d.Release()

		adapter.OnError(ctx, idgen.New(), &a2a.Error{Code: -32000, Message: "boom"}, []byte(`{}`))
		if !d.Kept() {
			t.Fatal("the error frame was not kept for redelivery")
		}
		if n := log.completes.Load(); n != 0 {
			t.Fatalf("Complete called %d times, want 0", n)
		}
	})
}
