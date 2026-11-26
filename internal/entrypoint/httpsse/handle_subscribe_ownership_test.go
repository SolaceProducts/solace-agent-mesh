// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func subscribeThroughMux(t *testing.T, a *HTTPSSEAdapter, taskID, caller string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sse/subscribe/"+taskID, nil).WithContext(ctx)
	req = withIdentity(req, caller, scopes...)

	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func seedTaskOwnedBy(t *testing.T, a *HTTPSSEAdapter, owner string) string {
	t.Helper()
	return seedTask(t, store.NewTaskRepo(a.core.Store()), owner, "completed")
}

func assertStreamed(t *testing.T, rec *httptest.ResponseRecorder, who string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: got status %d, want 200", who, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("%s: Content-Type = %q, want text/event-stream", who, ct)
	}
	if !strings.Contains(rec.Body.String(), "SSE connection established") {
		t.Errorf("%s: body did not contain the SSE preamble: %q", who, rec.Body.String())
	}
}

func TestSubscribeOwnership_TasksRowIsAuthority(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	const owner = "owner@example.com"
	const other = "other@example.com"

	taskID := seedTaskOwnedBy(t, adapter, owner)
	adapter.sseManager.RegisterTask(taskID, "sess", owner)
	_ = core

	t.Run("owner streams", func(t *testing.T) {
		assertStreamed(t, subscribeThroughMux(t, adapter, taskID, owner), "owner")
	})

	t.Run("cross-user is 404", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, other)
		if rec.Code != http.StatusNotFound {
			t.Errorf("cross-user got %d, want 404", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "SSE connection established") {
			t.Error("cross-user received the SSE preamble — the stream was attached")
		}
	})

	t.Run("activity scope streams", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, other, rbac.ScopeActivityRead)
		assertStreamed(t, rec, "activity-scope holder")
	})

	t.Run("empty scope set is not elevated", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, other)
		if rec.Code != http.StatusNotFound {
			t.Errorf("empty-scope caller got %d, want 404", rec.Code)
		}
	})
}

func TestSubscribeOwnership_SSEManagerIsAuthorityWhenNoRowExists(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	const owner = "owner@example.com"
	const other = "other@example.com"

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "sess", owner)

	t.Run("owner streams", func(t *testing.T) {
		assertStreamed(t, subscribeThroughMux(t, adapter, taskID, owner), "owner")
	})

	t.Run("cross-user is 404", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, other)
		if rec.Code != http.StatusNotFound {
			t.Errorf("cross-user got %d, want 404 — the SSE manager's owner was not consulted", rec.Code)
		}
	})

	t.Run("activity scope streams", func(t *testing.T) {
		assertStreamed(t, subscribeThroughMux(t, adapter, taskID, other, rbac.ScopeActivityRead), "activity-scope holder")
	})
}

func TestSubscribeOwnership_UnknownTaskReachesHandler(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)

	rec := subscribeThroughMux(t, adapter, idgen.New(), "anyone@example.com")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown task got %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SSE connection established") {
		t.Error("unknown task should not have attached a stream")
	}
}

func TestSubscribeOwnership_MalformedTaskIDIsRejectedBeforeTheQuery(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)

	if _, err := core.Store().Exec("ALTER TABLE tasks RENAME TO tasks_hidden_by_test"); err != nil {
		t.Fatalf("hide tasks table: %v", err)
	}

	for _, taskID := range []string{"garbage", "not-a-uuid", "01a0ab9c"} {
		t.Run(taskID, func(t *testing.T) {
			rec := subscribeThroughMux(t, adapter, taskID, "someone@example.com")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("got %d, want 400 — a malformed id must be refused on shape, not queried", rec.Code)
			}
		})
	}

	t.Run("control: a well-formed id does reach the query", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, idgen.New(), "someone@example.com")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("got %d, want 500 — if this is not 500 the preflight never queried and the cases above assert nothing", rec.Code)
		}
	})
}

func TestSubscribeOwnership_EndedTaskWithoutTasksRowUsesEventLogOwner(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)
	const owner = "owner@example.com"
	const other = "other@example.com"

	taskID := idgen.New()
	sessionID := idgen.New()
	rows := make([]*store.SSEEventBufferRow, 0, 3)
	for seq := 1; seq <= 3; seq++ {
		rows = append(rows, &store.SSEEventBufferRow{
			TaskID: taskID, SessionID: sessionID, UserID: owner,
			EventSequence: seq, EventType: "status_update", EventData: `{"n":1}`,
		})
	}
	if _, err := sseRepo.AppendBatch(context.Background(), rows); err != nil {
		t.Fatalf("seed event log: %v", err)
	}
	if err := sseRepo.MarkCompleted(context.Background(), taskID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if got, err := store.NewTaskRepo(core.Store()).GetByID(context.Background(), taskID); err != nil || got != nil {
		t.Fatalf("precondition: want no tasks row, got %v, %v", got, err)
	}

	t.Run("owner replays", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, owner)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "id: 3") {
			t.Errorf("owner got %d, body %q; want 200 with the full replay", rec.Code, rec.Body.String())
		}
	})

	t.Run("owner resumes with Last-Event-ID", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sse/subscribe/"+taskID, nil).WithContext(ctx)
		req.Header.Set("Last-Event-ID", "2")
		rec := httptest.NewRecorder()
		adapter.Mux().ServeHTTP(rec, withIdentity(req, owner))
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, "id: 3") || strings.Contains(body, "id: 2\n") {
			t.Errorf("resume got %d, body %q; want 200 with only events after 2", rec.Code, body)
		}
	})

	t.Run("second user gets 404", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, other)
		if rec.Code != http.StatusNotFound {
			t.Errorf("cross-user got %d, want 404", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "event:") {
			t.Error("cross-user received replayed events")
		}
	})

	t.Run("activity scope still reads", func(t *testing.T) {
		rec := subscribeThroughMux(t, adapter, taskID, other, rbac.ScopeActivityRead)
		if rec.Code != http.StatusOK {
			t.Errorf("activity-scope holder got %d, want 200", rec.Code)
		}
	})
}

func TestSubscribeOwnership_EventLogOwnerLookupFailureIsRefused(t *testing.T) {
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)
	const owner = "owner@example.com"

	taskID := idgen.New()
	rows := []*store.SSEEventBufferRow{{
		TaskID: taskID, SessionID: idgen.New(), UserID: owner,
		EventSequence: 1, EventType: "status_update", EventData: `{"n":1}`,
	}}
	if _, err := sseRepo.AppendBatch(t.Context(), rows); err != nil {
		t.Fatalf("seed event log: %v", err)
	}
	if _, err := core.Store().Exec("ALTER TABLE sse_event_buffer RENAME TO sse_event_buffer_hidden_by_test"); err != nil {
		t.Fatalf("hide event log: %v", err)
	}

	rec := subscribeThroughMux(t, adapter, taskID, owner)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "event:") {
		t.Error("a failed owner lookup still streamed events")
	}
}
