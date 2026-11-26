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
)

const execCanary = "CANARY-scheduled-execution-content"

func seedExecutionForOwner(t *testing.T, h *scheduledTaskHandler, owner *string) (a2aTaskID string) {
	t.Helper()
	ctx := context.Background()

	task := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               "nightly-" + idgen.New(),
		Namespace:          "test-ns",
		UserID:             owner,
		CreatedBy:          "creator@example.com",
		ScheduleType:       "cron",
		ScheduleExpression: "0 0 * * *",
		Timezone:           "UTC",
		TargetAgentName:    "agent",
		TargetType:         "agent",
		TaskMessage:        `[{"kind":"text","text":"CANARY-scheduled-execution-content"}]`,
		Enabled:            true,
	}
	if err := h.repo.Create(ctx, task); err != nil {
		t.Fatalf("create scheduled task: %v", err)
	}

	a2aTaskID = idgen.New()
	snapshot := `{"promptText":"` + execCanary + `"}`
	exec := &store.ScheduledTaskExecution{
		ID:              idgen.New(),
		ScheduledTaskID: task.ID,
		Status:          "completed",
		A2ATaskID:       &a2aTaskID,
		ScheduledFor:    1000,
		TaskSnapshot:    &snapshot,
	}
	if err := h.repo.CreateExecution(ctx, exec); err != nil {
		t.Fatalf("create execution: %v", err)
	}
	return a2aTaskID
}

func getExecByA2A(t *testing.T, mux *http.ServeMux, a2aTaskID, caller string) *httptest.ResponseRecorder {
	t.Helper()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/scheduledTasks/executions/byA2aTask/"+a2aTaskID, nil), caller)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestExecutionByA2ATask_OwnerIsServedStrangerIsRefused(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	owner := "owner@example.com"
	a2aTaskID := seedExecutionForOwner(t, h, &owner)

	t.Run("owner is served", func(t *testing.T) {
		rec := getExecByA2A(t, mux, a2aTaskID, owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("owner got %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), execCanary) {
			t.Errorf("owner did not receive their own execution snapshot: %s", rec.Body.String())
		}
	})

	t.Run("stranger is refused and sees nothing", func(t *testing.T) {
		rec := getExecByA2A(t, mux, a2aTaskID, "stranger@example.com")
		if rec.Code != http.StatusNotFound {
			t.Errorf("stranger got %d, want 404", rec.Code)
		}
		if strings.Contains(rec.Body.String(), execCanary) {
			t.Errorf("stranger received the owner's prompt: %s", rec.Body.String())
		}
	})
}

func TestExecutionByA2ATask_NamespaceLevelTaskStaysReadable(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	a2aTaskID := seedExecutionForOwner(t, h, nil)

	rec := getExecByA2A(t, mux, a2aTaskID, "anyone@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("namespace-level execution got %d for an ordinary caller, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestExecutionByA2ATask_DeletedParentIsRefused(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	owner := "owner@example.com"
	a2aTaskID := seedExecutionForOwner(t, h, &owner)

	exec, err := h.repo.GetExecutionByA2ATaskID(context.Background(), a2aTaskID)
	if err != nil || exec == nil {
		t.Fatalf("load seeded execution: %v", err)
	}
	if err := h.repo.SoftDelete(context.Background(), exec.ScheduledTaskID, "owner@example.com"); err != nil {
		t.Fatalf("soft delete parent task: %v", err)
	}

	rec := getExecByA2A(t, mux, a2aTaskID, owner)
	if rec.Code != http.StatusNotFound {
		t.Errorf("execution with a deleted parent got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func seedTaskAndExecFor(t *testing.T, h *scheduledTaskHandler, owner *string) (taskID, a2aTaskID string) {
	t.Helper()
	a2aTaskID = seedExecutionForOwner(t, h, owner)
	exec, err := h.repo.GetExecutionByA2ATaskID(context.Background(), a2aTaskID)
	if err != nil || exec == nil {
		t.Fatalf("load seeded execution: %v", err)
	}
	return exec.ScheduledTaskID, a2aTaskID
}

func getAs(t *testing.T, mux *http.ServeMux, path, caller string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, withIdentity(httptest.NewRequest(http.MethodGet, path, nil), caller))
	return rec
}

func TestScheduledTaskReads_OwnerOrNamespaceOnly(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	owner := "owner@example.com"
	ownedID, _ := seedTaskAndExecFor(t, h, &owner)
	sharedID, _ := seedTaskAndExecFor(t, h, nil)

	for _, tc := range []struct {
		name string
		path func(string) string
	}{
		{"get task", func(id string) string { return "/api/v1/scheduledTasks/" + id }},
		{"list executions", func(id string) string { return "/api/v1/scheduledTasks/" + id + "/executions" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := getAs(t, mux, tc.path(ownedID), owner); rec.Code != http.StatusOK {
				t.Errorf("owner got %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			rec := getAs(t, mux, tc.path(ownedID), "stranger@example.com")
			if rec.Code != http.StatusNotFound {
				t.Errorf("stranger got %d, want 404; body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), execCanary) {
				t.Errorf("stranger received the owner's task content: %s", rec.Body.String())
			}
			if rec := getAs(t, mux, tc.path(sharedID), "anyone@example.com"); rec.Code != http.StatusOK {
				t.Errorf("namespace-level task got %d for an ordinary caller, want 200 — the config-driven path regressed; body=%s",
					rec.Code, rec.Body.String())
			}
		})
	}
}

func TestScheduledTaskList_ExcludesOtherUsersTasks(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	theirs := "theirs@example.com"
	mine := "mine@example.com"
	otherID, _ := seedTaskAndExecFor(t, h, &theirs)
	myID, _ := seedTaskAndExecFor(t, h, &mine)
	sharedID, _ := seedTaskAndExecFor(t, h, nil)

	rec := getAs(t, mux, "/api/v1/scheduledTasks", mine)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, otherID) {
		t.Errorf("list included another user's task %s: %s", otherID, body)
	}
	if !strings.Contains(body, myID) {
		t.Errorf("list omitted the caller's own task %s: %s", myID, body)
	}
	if !strings.Contains(body, sharedID) {
		t.Errorf("list omitted the unowned namespace-level task %s — that is the half of the marker the config path depends on: %s", sharedID, body)
	}
}

func seedExecViaAdapter(t *testing.T, a *HTTPSSEAdapter, owner string) (execID string) {
	t.Helper()
	ctx := context.Background()
	repo := store.NewScheduledTaskRepo(a.core.Store())

	ownerPtr := &owner
	task := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               "premux-" + idgen.New(),
		Namespace:          "test-ns",
		UserID:             ownerPtr,
		CreatedBy:          owner,
		ScheduleType:       "cron",
		ScheduleExpression: "0 0 * * *",
		Timezone:           "UTC",
		TargetAgentName:    "agent",
		TargetType:         "agent",
		TaskMessage:        `[{"kind":"text","text":"` + execCanary + `"}]`,
		Enabled:            true,
	}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("create scheduled task: %v", err)
	}

	execID = idgen.New()
	snapshot := `{"promptText":"` + execCanary + `"}`
	a2aID := idgen.New()
	if err := repo.CreateExecution(ctx, &store.ScheduledTaskExecution{
		ID:              execID,
		ScheduledTaskID: task.ID,
		Status:          "completed",
		A2ATaskID:       &a2aID,
		ScheduledFor:    1000,
		TaskSnapshot:    &snapshot,
	}); err != nil {
		t.Fatalf("create execution: %v", err)
	}
	return execID
}

func dispatchExec(t *testing.T, a *HTTPSSEAdapter, method, execID, caller string) *httptest.ResponseRecorder {
	t.Helper()
	req := withIdentity(httptest.NewRequest(method, "/api/v1/scheduledTasks/executions/"+execID, nil), caller)
	rec := httptest.NewRecorder()
	a.preMuxDispatch(a.Mux()).ServeHTTP(rec, req)
	return rec
}

func TestScheduledExecutionByID_OwnerReadsStrangerRefused(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	execID := seedExecViaAdapter(t, adapter, "exec-owner@example.com")

	rec := dispatchExec(t, adapter, http.MethodGet, execID, "exec-owner@example.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), execCanary) {
		t.Errorf("owner got 200 but no execution content; body=%s", rec.Body.String())
	}

	rec = dispatchExec(t, adapter, http.MethodGet, execID, "exec-stranger@example.com")
	if rec.Code != http.StatusNotFound {
		t.Errorf("stranger got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), execCanary) {
		t.Errorf("stranger received the owner's execution content: %s", rec.Body.String())
	}
}

func TestScheduledExecutionByID_StrangerCannotDelete(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	execID := seedExecViaAdapter(t, adapter, "exec-owner@example.com")

	rec := dispatchExec(t, adapter, http.MethodDelete, execID, "exec-stranger@example.com")
	if rec.Code != http.StatusNotFound {
		t.Errorf("stranger delete got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}

	rec = dispatchExec(t, adapter, http.MethodGet, execID, "exec-owner@example.com")
	if rec.Code != http.StatusOK {
		t.Errorf("after a refused delete the owner got %d, want 200 — the delete was not actually refused", rec.Code)
	}
}

func TestScheduledExecutionByID_IsRegistered(t *testing.T) {
	adapter := newCoverageAdapter(t)
	want := map[string]bool{
		"GET /api/v1/scheduledTasks/executions/{id}":    false,
		"DELETE /api/v1/scheduledTasks/executions/{id}": false,
	}
	for _, r := range adapter.Routes() {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("%s is dispatched in production but absent from the route registry, so every route invariant is blind to it", key)
		}
	}
}

func seedExecutionInNamespace(t *testing.T, h *scheduledTaskHandler, owner, namespace string) (taskID, a2aTaskID string) {
	t.Helper()
	ctx := context.Background()

	task := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               "foreign-" + idgen.New(),
		Namespace:          namespace,
		UserID:             &owner,
		CreatedBy:          owner,
		ScheduleType:       "cron",
		ScheduleExpression: "0 0 * * *",
		Timezone:           "UTC",
		TargetAgentName:    "agent",
		TargetType:         "agent",
		TaskMessage:        `[{"kind":"text","text":"` + execCanary + `"}]`,
		Enabled:            true,
	}
	if err := h.repo.Create(ctx, task); err != nil {
		t.Fatalf("create scheduled task: %v", err)
	}

	a2aTaskID = idgen.New()
	snapshot := `{"promptText":"` + execCanary + `"}`
	if err := h.repo.CreateExecution(ctx, &store.ScheduledTaskExecution{
		ID:              idgen.New(),
		ScheduledTaskID: task.ID,
		Status:          "completed",
		A2ATaskID:       &a2aTaskID,
		ScheduledFor:    1000,
		TaskSnapshot:    &snapshot,
	}); err != nil {
		t.Fatalf("create execution: %v", err)
	}
	return task.ID, a2aTaskID
}

func TestExecutionByA2ATask_WrongNamespaceGets404(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	const owner = "owner@example.com"
	_, a2aTaskID := seedExecutionInNamespace(t, h, owner, "other-ns")

	rec := getExecByA2A(t, mux, a2aTaskID, owner)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("owner in another namespace got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), execCanary) {
		t.Errorf("a foreign-namespace execution snapshot was served: %s", rec.Body.String())
	}
}

func TestScheduledTaskGet_WrongNamespaceGets404(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	const owner = "owner@example.com"
	taskID, _ := seedExecutionInNamespace(t, h, owner, "other-ns")

	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/scheduledTasks/"+taskID, nil), owner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("owner in another namespace got %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), execCanary) {
		t.Errorf("a foreign-namespace task was served: %s", rec.Body.String())
	}
}

func TestScheduledTaskGet_OwnNamespaceIsServed(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	const owner = "owner@example.com"
	taskID, a2aTaskID := seedExecutionInNamespace(t, h, owner, "test-ns")

	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/scheduledTasks/"+taskID, nil), owner)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner in the gateway namespace got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if rec := getExecByA2A(t, mux, a2aTaskID, owner); rec.Code != http.StatusOK {
		t.Fatalf("byA2aTask for the owner in the gateway namespace got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
