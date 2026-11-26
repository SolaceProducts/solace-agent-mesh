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

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestTaskProducers_ScheduledTaskIsReadableByItsOwner(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	const creator = "scheduler-user@example.com"

	owner := "explicit-owner@example.com"
	scheduled := &store.ScheduledTask{UserID: &owner, CreatedBy: creator}
	if got := scheduled.ResolveOwnerUserID(); got != owner {
		t.Fatalf("ResolveOwnerUserID() = %q, want the explicit UserID %q", got, owner)
	}

	fallback := &store.ScheduledTask{CreatedBy: creator}
	if got := fallback.ResolveOwnerUserID(); got != creator {
		t.Fatalf("ResolveOwnerUserID() = %q, want the CreatedBy fallback %q", got, creator)
	}

	for _, tc := range []struct{ name, rowOwner string }{
		{"explicit owner", owner},
		{"createdBy fallback", creator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskID := seedTaskForUser(t, adapter, tc.rowOwner)

			rec := getTaskAs(t, adapter, taskID, tc.rowOwner)
			if rec.Code != http.StatusOK {
				t.Errorf("owner of a fired scheduled task got %d, want 200 — the ownership check locked them out of their own result; body=%s",
					rec.Code, rec.Body.String())
			}
			if rec := getTaskAs(t, adapter, taskID, "someone-else@example.com"); rec.Code != http.StatusNotFound {
				t.Errorf("stranger got %d, want 404", rec.Code)
			}
		})
	}
}

func TestTaskProducers_SystemOwnedTaskNeedsTheReadScope(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)

	ranAs := "system:nightly"
	systemOwned := (&store.ScheduledTaskExecution{RunAs: &ranAs}).ArtifactUserID(&store.ScheduledTask{CreatedBy: "alice@example.com"})
	if systemOwned != ranAs {
		t.Fatalf("ArtifactUserID() = %q, want %s", systemOwned, ranAs)
	}
	taskID := seedTaskForUser(t, adapter, systemOwned)

	if rec := getTaskAs(t, adapter, taskID, "alice@example.com"); rec.Code != http.StatusNotFound {
		t.Errorf("the schedule's creator got %d, want 404 for a task a service account ran", rec.Code)
	}

	if rec := getTaskAs(t, adapter, taskID, "any-user@example.com"); rec.Code != http.StatusNotFound {
		t.Errorf("ordinary caller got %d, want 404 for a system-owned task", rec.Code)
	}
	if rec := getTaskAs(t, adapter, taskID, "auditor@example.com", rbac.ScopeActivityRead); rec.Code != http.StatusOK {
		t.Errorf("activity:*:read holder got %d, want 200 — a system-owned task must stay reachable by the Activity Monitor; body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestTaskProducers_ListRoutesReturnOnlyTheCallersTasks(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	const mine = "mine@example.com"
	const theirs = "theirs@example.com"

	myTask := seedTaskForUser(t, adapter, mine)
	theirTask := seedBackgroundTaskForUser(t, adapter, theirs)
	myBackground := seedBackgroundTaskForUser(t, adapter, mine)

	for _, path := range []string{"/api/v1/tasks", "/api/v1/tasks/background/active"} {
		t.Run(path, func(t *testing.T) {
			req := withIdentity(httptest.NewRequest(http.MethodGet, path, nil), mine)
			rec := httptest.NewRecorder()
			adapter.Mux().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, theirTask) {
				t.Errorf("caller received another user's task id %s: %s", theirTask, body)
			}
			if strings.Contains(body, theirs) {
				t.Errorf("caller received another user's identity: %s", body)
			}

			want := myTask
			if strings.HasSuffix(path, "/background/active") {
				want = myBackground
			}
			if !strings.Contains(body, want) {
				t.Errorf("caller's own task %s missing from %s: %s", want, path, body)
			}
		})
	}
}

func seedTaskForUser(t *testing.T, a *HTTPSSEAdapter, userID string) string {
	t.Helper()
	return createTaskRow(t, a, userID, "foreground", "completed")
}

func seedBackgroundTaskForUser(t *testing.T, a *HTTPSSEAdapter, userID string) string {
	t.Helper()
	return createTaskRow(t, a, userID, "background", "working")
}

func createTaskRow(t *testing.T, a *HTTPSSEAdapter, userID, mode, status string) string {
	t.Helper()
	taskID := idgen.New()
	st := status
	if err := store.NewTaskRepo(a.core.Store()).Create(context.Background(), &store.Task{
		ID:                         taskID,
		UserID:                     userID,
		StartTime:                  1000,
		Status:                     &st,
		ExecutionMode:              mode,
		BackgroundExecutionEnabled: mode == "background",
		Kind:                       store.TaskKindNormal,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskID
}

func getTaskAs(t *testing.T, a *HTTPSSEAdapter, taskID, caller string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID, nil), caller, scopes...)
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}
