// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

func newTestIndexer(artSvc *artifact.ScopedService) (*projectIndexer, *SSEManager) {
	mgr := NewSSEManager(100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ix := &projectIndexer{
		artSvc:     artSvc,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		sseManager: mgr,
		eventLog:   eventlog.NewMemoryEventLog(),
	}
	return ix, mgr
}

func drainIndexEvents(t *testing.T, ix *projectIndexer, taskID string) []string {
	t.Helper()
	events, err := ix.eventLog.Replay(context.Background(), taskID, 1)
	if err != nil {
		t.Fatalf("replay index events: %v", err)
	}
	var types []string
	for _, evt := range events {
		if evt.EventType != indexEventType {
			t.Errorf("unexpected SSE event type %q (want %q)", evt.EventType, indexEventType)
		}
		var p map[string]any
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			t.Fatalf("decode index event %q: %v", evt.Payload, err)
		}
		typ, _ := p["type"].(string)
		types = append(types, typ)
	}
	return types
}

func TestPublishIndexEvent_AppendsPublishesAndCompletes(t *testing.T) {
	ix, mgr := newTestIndexer(nil)
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "session-1", "user-1")

	liveCh, connID, ok := mgr.Subscribe(taskID)
	if !ok {
		t.Fatal("Subscribe: registered task should exist")
	}
	defer mgr.Unsubscribe(taskID, connID)

	ix.publishIndexEvent(taskID, map[string]any{"type": "indexing_started"}, false)
	ix.publishIndexEvent(taskID, map[string]any{"type": "task_completed"}, true)

	for i, want := range []string{"indexing_started", "task_completed"} {
		select {
		case evt := <-liveCh:
			if evt.EventType != indexEventType {
				t.Errorf("live event %d type = %q, want %q", i, evt.EventType, indexEventType)
			}
			var p map[string]any
			if err := json.Unmarshal(evt.Data, &p); err != nil {
				t.Fatalf("decode live event %d: %v", i, err)
			}
			if p["type"] != want {
				t.Errorf("live event %d payload type = %v, want %v", i, p["type"], want)
			}
			if evt.Sequence != int64(i+1) {
				t.Errorf("live event %d sequence = %d, want %d", i, evt.Sequence, i+1)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("live event %d (%s) was not fanned out", i, want)
		}
	}

	if got := drainIndexEvents(t, ix, taskID); !slices.Equal(got, []string{"indexing_started", "task_completed"}) {
		t.Fatalf("durable replay = %v, want [indexing_started task_completed]", got)
	}

	ch, err := ix.eventLog.Subscribe(context.Background(), taskID, 1)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	replayed := 0
	for {
		select {
		case _, open := <-ch:
			if !open {
				if replayed != 2 {
					t.Fatalf("Subscribe replayed %d events before close, want 2", replayed)
				}
				return
			}
			replayed++
		case <-time.After(2 * time.Second):
			t.Fatal("Subscribe channel never closed — terminal event did not Complete the task")
		}
	}
}

func TestRebuildIndex_PublishesTaskCompletedOnSuccess(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	const userID, projectID = "alice", "proj-ok"
	loc := artSvc.Resolve("", userID, "project-"+projectID, "notes.txt")
	if _, err := store.Save(context.Background(), loc, []byte("pluto is a dwarf planet"), "text/plain"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ix, mgr := newTestIndexer(artSvc)
	const taskID = "11111111-1111-1111-1111-111111111111"
	mgr.RegisterTask(taskID, "project-"+projectID, userID)
	ix.rebuildIndex(context.Background(), projectID, userID, "", taskID)

	got := drainIndexEvents(t, ix, taskID)
	if len(got) != 1 || got[0] != "task_completed" {
		t.Fatalf("want [task_completed], got %v", got)
	}
}

func TestRebuildIndex_PublishesConversionFailedThenTerminal(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	const userID, projectID = "alice", "proj-convfail"
	session := "project-" + projectID
	bad := artSvc.Resolve("", userID, session, "broken.docx")
	if _, err := store.Save(context.Background(), bad, []byte("not a docx"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document"); err != nil {
		t.Fatalf("seed bad: %v", err)
	}
	good := artSvc.Resolve("", userID, session, "ok.txt")
	if _, err := store.Save(context.Background(), good, []byte("indexable text"), "text/plain"); err != nil {
		t.Fatalf("seed good: %v", err)
	}

	ix, mgr := newTestIndexer(artSvc)
	const taskID = "22222222-2222-2222-2222-222222222222"
	mgr.RegisterTask(taskID, session, userID)
	ix.rebuildIndex(context.Background(), projectID, userID, "", taskID)

	got := drainIndexEvents(t, ix, taskID)
	if len(got) != 2 || got[0] != "conversion_failed" || got[1] != "task_completed" {
		t.Fatalf("want [conversion_failed task_completed], got %v", got)
	}
}

type saveFailingService struct {
	artifact.Service
}

func (saveFailingService) Save(context.Context, artifact.Locator, []byte, string) (int, error) {
	return 0, errors.New("disk full")
}

func (saveFailingService) SaveVersion(context.Context, artifact.Locator, int, []byte, string) error {
	return errors.New("disk full")
}

func TestRebuildIndex_PublishesIndexingFailedOnSaveError(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: saveFailingService{store}, Scope: "namespace", Namespace: "test-ns"}
	const userID, projectID = "alice", "proj-savefail"
	loc := artSvc.Resolve("", userID, "project-"+projectID, "notes.txt")
	if _, err := store.Save(context.Background(), loc, []byte("indexable"), "text/plain"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ix, mgr := newTestIndexer(artSvc)
	const taskID = "33333333-3333-3333-3333-333333333333"
	mgr.RegisterTask(taskID, "project-"+projectID, userID)
	ix.rebuildIndex(context.Background(), projectID, userID, "", taskID)

	got := drainIndexEvents(t, ix, taskID)
	if len(got) != 2 || got[0] != "indexing_failed" || got[1] != "task_completed" {
		t.Fatalf("want [indexing_failed task_completed], got %v", got)
	}
}

func TestRebuildIndex_PublishesTaskErrorOnPanic(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	const userID, projectID = "alice", "proj-panic"

	ix, mgr := newTestIndexer(artSvc)
	ix.pdfExtractorFactory = func(string, string, string) convert.PDFExtractor {
		panic("boom in factory")
	}
	const taskID = "44444444-4444-4444-4444-444444444444"
	mgr.RegisterTask(taskID, "project-"+projectID, userID)
	ix.rebuildIndex(context.Background(), projectID, userID, "", taskID)

	got := drainIndexEvents(t, ix, taskID)
	if len(got) != 1 || got[0] != "task_error" {
		t.Fatalf("want [task_error], got %v", got)
	}
}

func TestStartIndexRebuild_SetsSseLocationHeaderWithUUID(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	ix, _ := newTestIndexer(artSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/projects/p/artifacts", nil)

	taskID := ix.startIndexRebuild(rec, req, "proj-hdr", "alice")
	if taskID == "" {
		t.Fatal("expected non-empty taskID when SSE manager is wired")
	}
	loc := rec.Header().Get("Sse-Location")
	const prefix = "/api/v1/sse/subscribe/"
	if len(loc) <= len(prefix) || loc[:len(prefix)] != prefix {
		t.Fatalf("Sse-Location %q missing prefix %q", loc, prefix)
	}
	if got := loc[len(prefix):]; got != taskID {
		t.Fatalf("header taskID %q != returned %q", got, taskID)
	}
	if _, err := uuid.Parse(taskID); err != nil {
		t.Fatalf("taskID %q is not a UUID (subscribe endpoint requireUUID would reject): %v", taskID, err)
	}
}

func TestStartIndexRebuild_NoManagerNoHeader(t *testing.T) {
	store := memory.New()
	artSvc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	ix := &projectIndexer{
		artSvc: artSvc,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/projects/p/artifacts/x.txt", nil)

	if taskID := ix.startIndexRebuild(rec, req, "proj-nomgr", "alice"); taskID != "" {
		t.Fatalf("expected empty taskID without SSE manager, got %q", taskID)
	}
	if loc := rec.Header().Get("Sse-Location"); loc != "" {
		t.Fatalf("expected no Sse-Location header, got %q", loc)
	}
}
