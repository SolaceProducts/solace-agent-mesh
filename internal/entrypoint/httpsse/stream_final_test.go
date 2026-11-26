// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestFinishTask_FinalWithNoRowReachesTheStream(t *testing.T) {
	for _, viaError := range []bool{false, true} {
		t.Run(map[bool]string{false: "final response", true: "error"}[viaError], func(t *testing.T) {
			adapter, _, _, _, _ := setupTestAdapterWithDB(t)
			taskID := idgen.New()
			ctx := t.Context()
			if _, err := adapter.eventLog.Append(ctx, taskID, idgen.New(), "u", "status_update", statusPayload(1)); err != nil {
				t.Fatal(err)
			}
			adapter.sseManager.RegisterTask(taskID, "", "")
			ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog})
			frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
			if fr := nextFrame(t, frames, 3*time.Second); fr.id != "1" {
				t.Fatalf("first frame = %+v", fr)
			}

			if _, err := adapter.eventLog.Append(ctx, taskID, idgen.New(), "u", "status_update", statusPayload(2)); err != nil {
				t.Fatal(err)
			}
			if viaError {
				adapter.OnError(ctx, taskID, &a2a.Error{}, finalPayload())
			} else {
				adapter.OnFinalResponse(ctx, taskID, &a2a.Task{}, finalPayload())
			}
			if fr := nextFrame(t, frames, 3*time.Second); fr.id != "2" {
				t.Fatalf("frame = %+v, want row 2 before the final", fr)
			}
			if fr := nextFrame(t, frames, 3*time.Second); fr.eventType != "final_response" || fr.id != "" {
				t.Fatalf("frame = %+v, want the final with no id", fr)
			}
			recvWithin(t, ended, "the stream did not end after the final")
			waitFor(t, "the entry was not released", func() bool { return !adapter.sseManager.HasTask(taskID) })
		})
	}
}

func TestServeFromEventLog_SubscribesAgainAfterAShed(t *testing.T) {
	el := eventlog.NewMemoryEventLog()
	mgr := NewSSEManager(1, nil)
	taskID := idgen.New()
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	w := &gateWriter{ResponseRecorder: httptest.NewRecorder(), gate: make(chan struct{}), blocked: make(chan struct{})}
	h := &subscribeHandler{sseManager: mgr, eventLog: el}
	ctx, cancel := context.WithCancel(t.Context())
	r := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/sse/subscribe/"+taskID, nil)
	done := make(chan struct{})
	go func() { defer close(done); h.serveFromEventLog(w, r, taskID, 1, w) }()
	defer func() { cancel(); recvWithin(t, done, "the stream did not end") }()
	waitFor(t, "the stream did not hold an entry", func() bool { return subscriberCount(mgr, taskID) == 1 })

	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"n":1}`)})
	recvWithin(t, w.blocked, "the first frame never reached the writer")
	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"n":2}`)})
	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"n":3}`)})
	close(w.gate)

	waitFor(t, "the stream did not hold the entry again after the shed", func() bool { return subscriberCount(mgr, taskID) == 1 })
	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"probe":true}`)})
	waitFor(t, "a frame after the shed did not reach the stream", func() bool { return strings.Contains(w.body(), "probe") })
}

func TestStreamLive_FinalRowReleasesTheEntryWhenTheClientLeaves(t *testing.T) {
	adapter, core, _, _, repo := setupTestAdapterWithDB(t)
	writer := eventlog.NewPersistentEventLog(repo, store.NewTaskRepo(core.Store()), slog.Default())
	taskID, sessionID := idgen.New(), idgen.New()
	adapter.eventLog.(*eventlog.PersistentEventLog).SetTaskRunningCheck(func(string) (bool, bool) { return true, true })
	adapter.sseManager.RegisterTask(taskID, sessionID, "user-1")
	if _, err := writer.Append(t.Context(), taskID, sessionID, "user-1", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	if fr := nextFrame(t, frames, 3*time.Second); fr.id != "1" {
		t.Fatalf("first frame = %+v", fr)
	}

	if _, err := writer.Append(t.Context(), taskID, sessionID, "user-1", "final_response", finalPayload()); err != nil {
		t.Fatal(err)
	}
	if fr := nextFrame(t, frames, 3*time.Second); fr.eventType != "final_response" {
		t.Fatalf("frame = %+v, want the final row the sweep finds", fr)
	}
	cancel()
	recvWithin(t, ended, "the stream did not end")
	waitFor(t, "the entry stayed after the final row", func() bool { return !adapter.sseManager.HasTask(taskID) })
}

type failFinalIndexLog struct{ eventlog.TaskEventLog }

func (l failFinalIndexLog) Append(ctx context.Context, taskID, sessionID, userID, eventType string, payload []byte) (int64, error) {
	if strings.Contains(string(payload), "task_completed") {
		return 0, errors.New("insert failed")
	}
	return l.TaskEventLog.Append(ctx, taskID, sessionID, userID, eventType, payload)
}

func TestPublishIndexEvent_FinalWithNoRowReachesTheStream(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	el := failFinalIndexLog{adapter.eventLog}
	ix := &projectIndexer{logger: slog.New(slog.DiscardHandler), sseManager: adapter.sseManager, eventLog: el}
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "project-"+idgen.New(), "user-1")
	ix.publishIndexEvent(taskID, map[string]any{"type": "indexing_started"}, false)
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: adapter.sseManager, eventLog: el})
	frames, ended := liveFeed(t.Context(), t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	if fr := nextFrame(t, frames, 3*time.Second); fr.id != "1" {
		t.Fatalf("first frame = %+v", fr)
	}

	ix.publishIndexEvent(taskID, map[string]any{"type": "task_completed"}, true)
	if fr := nextFrame(t, frames, 3*time.Second); fr.eventType != indexEventType || !strings.Contains(fr.data, "task_completed") || fr.id != "" {
		t.Fatalf("frame = %+v, want the final index event with no id", fr)
	}
	recvWithin(t, ended, "the stream did not end after the final")
	waitFor(t, "the entry was not released", func() bool { return !adapter.sseManager.HasTask(taskID) })
}
