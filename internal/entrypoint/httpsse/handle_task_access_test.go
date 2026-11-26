// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func taskAccessMux(h *taskHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/background/active", h.handleActiveBackground)
	mux.HandleFunc("GET /api/v1/tasks", h.handleList)
	mux.HandleFunc("GET /api/v1/tasks/{id}", h.handleGetTask)
	return mux
}

func taskIdentity(email string) *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{
		UserID:        email,
		Email:         email,
		Authenticated: true,
	})
}

func withTaskIdentity(req *http.Request, id *auth.Subject) *http.Request {
	return req.WithContext(auth.ContextWithSubject(req.Context(), id))
}

func seedTask(t *testing.T, repo *store.TaskRepo, userID string, status ...string) string {
	t.Helper()
	taskID := idgen.New()
	task := &store.Task{
		ID:            taskID,
		UserID:        userID,
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground",
		Kind:          store.TaskKindNormal,
	}
	if len(status) > 0 {
		task.Status = &status[0]
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("seed task for %q: %v", userID, err)
	}
	return taskID
}

func setupTaskAccessHandler(t *testing.T) (*taskHandler, *store.TaskRepo) {
	t.Helper()
	adapter, core, _, _, sseRepo := setupTestAdapterWithDB(t)
	db := core.Store()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	h := &taskHandler{
		core:      core,
		taskRepo:  taskRepo,
		eventRepo: eventRepo,
		sseRepo:   sseRepo,
		logger:    adapter.logger,
	}
	return h, taskRepo
}

func TestGetTask_CrossUser_Returns404(t *testing.T) {
	h, repo := setupTaskAccessHandler(t)
	mux := taskAccessMux(h)

	bobID := seedTask(t, repo, "bob@example.com")

	req := httptest.NewRequest("GET", "/api/v1/tasks/"+bobID, nil)
	req = withTaskIdentity(req, taskIdentity("alice@example.com"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-user GET, got %d; body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	var resp map[string]any
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	msg, _ := resp["message"].(string)
	if msg == "" {
		t.Error("expected a non-empty message field in error response")
	}
	if msg != "task not found" {
		t.Errorf("expected generic 'task not found', got %q", msg)
	}
}

func TestGetTask_Owner_Returns200(t *testing.T) {
	h, repo := setupTaskAccessHandler(t)
	mux := taskAccessMux(h)

	bobID := seedTask(t, repo, "bob@example.com")

	req := httptest.NewRequest("GET", "/api/v1/tasks/"+bobID, nil)
	req = withTaskIdentity(req, taskIdentity("bob@example.com"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner GET, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestListTasks_Isolation(t *testing.T) {
	h, repo := setupTaskAccessHandler(t)
	mux := taskAccessMux(h)

	_ = seedTask(t, repo, "alice@example.com")
	_ = seedTask(t, repo, "bob@example.com")

	req := httptest.NewRequest("GET", "/api/v1/tasks", nil)
	req = withTaskIdentity(req, taskIdentity("alice@example.com"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for list, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var page PaginatedResponse[map[string]any]
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	if len(page.Data) != 1 {
		t.Fatalf("expected 1 task for alice, got %d", len(page.Data))
	}

	gotUser, _ := page.Data[0]["userId"].(string)
	if gotUser != "alice@example.com" {
		t.Errorf("expected userId=alice@example.com, got %q", gotUser)
	}
}
