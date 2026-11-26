// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
)

func tid(slug string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("httpsse-test:"+slug)).String() //nolint:forbidigo // deterministic test ID
}

func setupScheduledTaskTest(t *testing.T, opts ...runtime.Option) (*scheduledTaskHandler, *http.ServeMux) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()

	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": testDBURL(t, "sched"),
		},
		"scheduler_service": map[string]any{
			"enabled": true,
		},
	}

	core := entrypoint.NewGatewayCore(adapter, appConfig)

	rt := runtime.NewRuntime(brk, nil, append([]runtime.Option{runtime.WithLogger(slog.Default())}, opts...)...)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	db := core.Store()
	if db == nil {
		t.Fatal("expected DB to be initialized")
	}
	t.Cleanup(func() { _ = db.Close() })

	schedService := core.ScheduledTasks()
	if schedService == nil {
		t.Fatal("expected scheduled task service to be initialized")
	}

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "agent"})
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "test-agent"})

	schedRepo := store.NewScheduledTaskRepo(db)
	h := &scheduledTaskHandler{
		core:        core,
		repo:        schedRepo,
		service:     schedService,
		sessKeyRepo: store.NewGatewaySessionKeyRepo(db),
		logger:      slog.Default(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/scheduledTasks", h.handleCreate)
	mux.HandleFunc("GET /api/v1/scheduledTasks", h.handleList)
	mux.HandleFunc("GET /api/v1/scheduledTasks/scheduler/status", h.handleSchedulerStatus)
	mux.HandleFunc("GET /api/v1/scheduledTasks/executions/recent", h.handleRecentExecutions)
	mux.HandleFunc("GET /api/v1/scheduledTasks/executions/byA2aTask/{a2aTaskId}", h.handleExecutionByA2ATask)
	mux.HandleFunc("POST /api/v1/scheduledTasks/preview", h.handlePreview)
	mux.HandleFunc("GET /api/v1/scheduledTasks/{id}", h.handleGet)
	mux.HandleFunc("PATCH /api/v1/scheduledTasks/{id}", h.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/scheduledTasks/{id}", h.handleDelete)
	mux.HandleFunc("POST /api/v1/scheduledTasks/{id}/trigger", h.handleTrigger)
	mux.HandleFunc("POST /api/v1/scheduledTasks/{id}/run", h.handleRun)
	mux.HandleFunc("POST /api/v1/scheduledTasks/{id}/enable", h.handleEnable)
	mux.HandleFunc("POST /api/v1/scheduledTasks/{id}/disable", h.handleDisable)
	mux.HandleFunc("GET /api/v1/scheduledTasks/{id}/executions", h.handleListExecutions)

	return h, mux
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}

func decodeResp[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(rec.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, rec.Body.String())
	}
	return v
}

func TestCreateScheduledTask(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Test Task",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        []map[string]string{{"type": "text", "text": "hello"}},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.Name != "Test Task" {
		t.Errorf("expected name 'Test Task', got %q", resp.Name)
	}
	if resp.ScheduleType != "cron" {
		t.Errorf("expected scheduleType 'cron', got %q", resp.ScheduleType)
	}
	if resp.Timezone != "UTC" {
		t.Errorf("expected timezone 'UTC', got %q", resp.Timezone)
	}
	if !resp.Enabled {
		t.Error("expected enabled to be true")
	}
	if resp.Source != "ui" {
		t.Errorf("expected source 'ui', got %q", resp.Source)
	}
	if resp.TargetType != "agent" {
		t.Errorf("expected targetType 'agent', got %q", resp.TargetType)
	}
}

func TestCreateScheduledTask_MissingRequired(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{"no name", map[string]any{"scheduleType": "cron", "scheduleExpression": "* * * * *", "targetAgentName": "a", "taskMessage": "hi"}, "name is required"},
		{"no scheduleType", map[string]any{"name": "x", "scheduleExpression": "* * * * *", "targetAgentName": "a", "taskMessage": "hi"}, "scheduleType is required"},
		{"no expression", map[string]any{"name": "x", "scheduleType": "cron", "targetAgentName": "a", "taskMessage": "hi"}, "scheduleExpression is required"},
		{"no agent", map[string]any{"name": "x", "scheduleType": "cron", "scheduleExpression": "* * * * *", "taskMessage": "hi"}, "targetAgentName is required"},
		{"no message", map[string]any{"name": "x", "scheduleType": "cron", "scheduleExpression": "* * * * *", "targetAgentName": "a"}, "taskMessage is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, tc.body))
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("expected error containing %q, got %q", tc.want, rec.Body.String())
			}
		})
	}
}

func TestCreateScheduledTask_InvalidExpression(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Bad Cron",
		"scheduleType":       "cron",
		"scheduleExpression": "invalid cron",
		"targetAgentName":    "agent",
		"taskMessage":        "hello",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateScheduledTask_MonthlyWeekdayPresets(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		expression string
		isWantDay  func(time.Time) bool
	}{
		{"first Monday", "0 9 * * 1#1", func(at time.Time) bool {
			return at.Weekday() == time.Monday && at.Day() <= 7
		}},
		{"last Friday", "0 9 * * 5L", func(at time.Time) bool {
			return at.Weekday() == time.Friday && at.AddDate(0, 0, 7).Month() != at.Month()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"name":               "Monthly " + tc.name,
				"scheduleType":       "cron",
				"scheduleExpression": tc.expression,
				"timezone":           "America/New_York",
				"targetAgentName":    "agent",
				"taskMessage":        "hello",
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body)))
			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}

			resp := decodeResp[scheduledTaskResponse](t, rec)
			if resp.NextRunTime == nil {
				t.Fatal("expected a next run time")
			}
			next, err := time.Parse(time.RFC3339, *resp.NextRunTime)
			if err != nil {
				t.Fatal(err)
			}
			local := next.In(ny)
			if !tc.isWantDay(local) || local.Hour() != 9 || local.Minute() != 0 {
				t.Fatalf("next run %s does not match %q", local, tc.expression)
			}
		})
	}

	t.Run("sixth Monday rejected", func(t *testing.T) {
		body := map[string]any{
			"name":               "Monthly sixth Monday",
			"scheduleType":       "cron",
			"scheduleExpression": "0 9 * * 1#6",
			"targetAgentName":    "agent",
			"taskMessage":        "hello",
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "occurrence after #") {
			t.Errorf("expected the # occurrence error, got %s", rec.Body.String())
		}
	})
}

func TestCreateScheduledTask_InvalidTimezone(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Bad TZ",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "agent",
		"taskMessage":        "hello",
		"timezone":           "Fake/Zone",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateScheduledTask_UnknownAgentRejected(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Doomed",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "NoSuchAgent",
		"taskMessage":        "hello",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a registered agent") {
		t.Errorf("expected error about unregistered agent, got %q", rec.Body.String())
	}
}

func TestCreateScheduledTask_WorkflowTargetSkipsAgentCheck(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Workflow Run",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "SomeWorkflow",
		"targetType":         "workflow",
		"taskMessage":        "hello",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateScheduledTask_UnknownAgentRejected(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "Existing", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `"hi"`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{"targetAgentName": "NoSuchAgent"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+task.ID, jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a registered agent") {
		t.Errorf("expected error about unregistered agent, got %q", rec.Body.String())
	}
}

func TestUpdateScheduledTask_TargetTypeChangedToAgentValidatesExistingName(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "WorkflowRow", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "SomeWorkflow", TargetType: "workflow",
		TaskMessage: `"hi"`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{"targetType": "agent"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+task.ID, jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not a registered agent") {
		t.Errorf("expected error about unregistered agent, got %q", rec.Body.String())
	}
}

func TestCreateScheduledTask_DuplicateNameConflict(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Dup Task",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        "hello",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("duplicate create: expected 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "already exists") {
		t.Errorf("expected 'already exists' message, got %q", rec2.Body.String())
	}
}

func TestUpdateScheduledTask_DuplicateNameConflict(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	mkTask := func(name string) *store.ScheduledTask {
		task := &store.ScheduledTask{
			ID: idgen.New(), Name: name, Namespace: "test-ns",
			UserID: &userID, CreatedBy: userID,
			ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
			Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
			TaskMessage: `"hi"`, Enabled: true, Source: "ui",
		}
		if err := h.repo.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		return task
	}
	mkTask("Taken")
	second := mkTask("Renamable")

	body := map[string]any{"name": "Taken"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+second.ID, jsonBody(t, body))
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("expected 'already exists' message, got %q", rec.Body.String())
	}
}

func TestCreateScheduledTask_SameNameDifferentOwners(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "ABC",
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        "hello",
	}
	createAs := func(userID string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := asUser(httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body)), userID)
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := createAs("user1"); rec.Code != http.StatusCreated {
		t.Fatalf("user1 create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := createAs("user2"); rec.Code != http.StatusCreated {
		t.Fatalf("user2 must be able to reuse another owner's name: got %d: %s", rec.Code, rec.Body.String())
	}

	for _, userID := range []string{"user1", "user2"} {
		rec := httptest.NewRecorder()
		req := asUser(httptest.NewRequest("GET", "/api/v1/scheduledTasks?pageSize=10", nil), userID)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s list: expected 200, got %d: %s", userID, rec.Code, rec.Body.String())
		}
		resp := decodeResp[PaginatedResponse[scheduledTaskResponse]](t, rec)
		if len(resp.Data) != 1 {
			t.Errorf("%s should see exactly their own task, got %d", userID, len(resp.Data))
		}
	}
}

func TestUpdateScheduledTask_RenameIntoOtherOwnersName(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	mkTask := func(owner, name string) *store.ScheduledTask {
		task := &store.ScheduledTask{
			ID: idgen.New(), Name: name, Namespace: "test-ns",
			UserID: &owner, CreatedBy: owner,
			ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
			Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
			TaskMessage: `"hi"`, Enabled: true, Source: "ui",
		}
		if err := h.repo.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		return task
	}
	mkTask("user1", "ABC")
	owned := mkTask("user2", "Renamable")

	rec := httptest.NewRecorder()
	req := asUser(httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+owned.ID,
		jsonBody(t, map[string]any{"name": "ABC"})), "user2")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("renaming into another owner's name must succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := decodeResp[scheduledTaskResponse](t, rec).Name; got != "ABC" {
		t.Errorf("name = %q, want ABC", got)
	}
}

func TestListScheduledTasks(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	for i := range 3 {
		task := &store.ScheduledTask{
			ID: idgen.New(), Name: fmt.Sprintf("Task %d", i), Namespace: "test-ns",
			UserID: &userID, CreatedBy: userID,
			ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
			Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
			TaskMessage: `[{"type":"text","text":"hi"}]`, Enabled: true, Source: "ui",
		}
		if err := h.repo.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks?pageSize=10", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[PaginatedResponse[scheduledTaskResponse]](t, rec)
	if len(resp.Data) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(resp.Data))
	}
	if resp.Meta.Pagination.Count != 3 {
		t.Errorf("expected total 3, got %v", resp.Meta.Pagination.Count)
	}
}

func TestListScheduledTasks_Pagination(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	for i := range 5 {
		task := &store.ScheduledTask{
			ID: idgen.New(), Name: fmt.Sprintf("Task %d", i), Namespace: "test-ns",
			UserID: &userID, CreatedBy: userID,
			ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
			Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
			TaskMessage: `[{"type":"text","text":"hi"}]`, Enabled: true, Source: "ui",
		}
		if err := h.repo.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks?pageSize=2&pageNumber=2", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[PaginatedResponse[scheduledTaskResponse]](t, rec)
	if len(resp.Data) != 2 {
		t.Errorf("expected 2 tasks on page 2, got %d", len(resp.Data))
	}
}

func TestGetScheduledTask(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "My Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "America/New_York", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/"+taskID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.ID != taskID {
		t.Errorf("expected id %q, got %q", taskID, resp.ID)
	}
	if resp.Timezone != "America/New_York" {
		t.Errorf("expected timezone 'America/New_York', got %q", resp.Timezone)
	}
}

func TestGetScheduledTask_NotFound(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/00000000-0000-0000-0000-000000000000", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestUpdateScheduledTask(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Original", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"name": "Updated",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.Name != "Updated" {
		t.Errorf("expected name 'Updated', got %q", resp.Name)
	}
}

func TestUpdateScheduledTask_ConfigSourceRestriction(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Config Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "config",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	t.Run("update enabled only - allowed", func(t *testing.T) {
		body := map[string]any{"enabled": false}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for enabled-only update, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update name - forbidden", func(t *testing.T) {
		body := map[string]any{"name": "New Name"}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for config task name update, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("update expression - forbidden", func(t *testing.T) {
		body := map[string]any{"scheduleExpression": "0 10 * * *"}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body))
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for config task expression update, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestDeleteScheduledTask(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Delete Me", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/scheduledTasks/"+taskID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/scheduledTasks/"+taskID, nil)
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec2.Code)
	}
}

func TestDeleteScheduledTask_ConfigSourceRestriction(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Config Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "config",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/scheduledTasks/"+taskID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for config task delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnableDisableScheduledTask(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Toggle Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/disable", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[map[string]any](t, rec)
	if resp["success"] != true {
		t.Error("expected success: true")
	}

	got, _ := h.repo.GetByID(context.Background(), taskID)
	if got.Enabled {
		t.Error("expected task to be disabled")
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/enable", nil)
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	got2, _ := h.repo.GetByID(context.Background(), taskID)
	if !got2.Enabled {
		t.Error("expected task to be enabled")
	}
}

func TestDisableScheduledTask_WithFailuresReportsPausedStatus(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Flaky Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Update(context.Background(), taskID, map[string]any{"consecutive_failure_count": 3}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/disable", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on disable, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/scheduledTasks/"+taskID, nil)
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 on get, got %d: %s", rec2.Code, rec2.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec2)
	if resp.Status != "paused" {
		t.Errorf("status = %q, want \"paused\" (paused wins over error)", resp.Status)
	}
}

func TestEnableScheduledTask_ResetsConsecutiveFailureCount(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Recovered Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: false, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Update(context.Background(), taskID, map[string]any{"consecutive_failure_count": 10}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/enable", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, _ := h.repo.GetByID(context.Background(), taskID)
	if !got.Enabled {
		t.Error("expected task to be enabled")
	}
	if got.ConsecutiveFailureCount != 0 {
		t.Errorf("consecutive_failure_count = %d, want 0 after re-enable", got.ConsecutiveFailureCount)
	}
}

func TestEnableDisableScheduledTask_ConfigSourceAllowed(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Config Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "config",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/disable", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for config task disable, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/enable", nil)
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for config task enable, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestListExecutions(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Exec Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		exec := &store.ScheduledTaskExecution{
			ID: idgen.New(), ScheduledTaskID: taskID,
			Status: "completed", ScheduledFor: int64(1000 + i),
		}
		if err := h.repo.CreateExecution(context.Background(), exec); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/"+taskID+"/executions", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[PaginatedResponse[executionResponse]](t, rec)
	if len(resp.Data) != 3 {
		t.Errorf("expected 3 executions, got %d", len(resp.Data))
	}
}

func TestRecentExecutions(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Recent Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	exec := &store.ScheduledTaskExecution{
		ID: idgen.New(), ScheduledTaskID: taskID,
		Status: "completed", ScheduledFor: 1000,
	}
	if err := h.repo.CreateExecution(context.Background(), exec); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/executions/recent", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[PaginatedResponse[executionResponse]](t, rec)
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 execution, got %d", len(resp.Data))
	}
}

func TestExecutionByA2ATask(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "A2A Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	a2aID := idgen.New()
	exec := &store.ScheduledTaskExecution{
		ID: idgen.New(), ScheduledTaskID: taskID,
		Status: "completed", ScheduledFor: 1000, A2ATaskID: &a2aID,
	}
	if err := h.repo.CreateExecution(context.Background(), exec); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/executions/byA2aTask/"+a2aID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[executionResponse](t, rec)
	if resp.ScheduledTaskID != taskID {
		t.Errorf("expected scheduledTaskId %q, got %q", taskID, resp.ScheduledTaskID)
	}
}

func TestExecutionByA2ATask_NotFound(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/executions/byA2aTask/nonexistent", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSchedulerStatus(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/scheduler/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[map[string]any](t, rec)
	if resp["namespace"] != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got %v", resp["namespace"])
	}
	if resp["schedulerRunning"] != true {
		t.Error("expected scheduler_running: true")
	}
}

func TestPreviewSchedule(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"timezone":           "UTC",
		"count":              3,
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/preview", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[map[string]any](t, rec)
	times, ok := resp["nextTimes"].([]any)
	if !ok || len(times) != 3 {
		t.Errorf("expected 3 next times, got %v", resp["nextTimes"])
	}
}

func TestPreviewSchedule_Invalid(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"scheduleType":       "cron",
		"scheduleExpression": "bad cron",
		"timezone":           "UTC",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/preview", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeletedTaskNotInList(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Soon Deleted", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	if err := h.repo.SoftDelete(context.Background(), taskID, userID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	resp := decodeResp[PaginatedResponse[scheduledTaskResponse]](t, rec)
	if len(resp.Data) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(resp.Data))
	}
}

func TestNamespaceTasksIncluded(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	task := &store.ScheduledTask{
		ID: idgen.New(), Name: "Namespace Task", Namespace: "test-ns",
		UserID: nil, CreatedBy: "system",
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "config",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks", nil)
	mux.ServeHTTP(rec, req)

	resp := decodeResp[PaginatedResponse[scheduledTaskResponse]](t, rec)
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 namespace task, got %d", len(resp.Data))
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/scheduledTasks?ownership=MINE", nil)
	mux.ServeHTTP(rec2, req2)

	resp2 := decodeResp[PaginatedResponse[scheduledTaskResponse]](t, rec2)
	if len(resp2.Data) != 0 {
		t.Errorf("expected 0 tasks with ownership=MINE, got %d", len(resp2.Data))
	}
}

func TestCreateScheduledTask_InvalidScheduleType(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	body := map[string]any{
		"name":               "Bad Type",
		"scheduleType":       "weekly",
		"scheduleExpression": "monday",
		"targetAgentName":    "agent",
		"taskMessage":        "hello",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUpdateScheduledTask_InvalidExpression(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Update Validate", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"scheduleExpression": "bad cron",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTriggerScheduledTask(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Trigger Me", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/trigger", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[map[string]any](t, rec)
	execID, ok := resp["id"].(string)
	if !ok || execID == "" {
		t.Fatal("expected non-empty id (execution id) in Operation response")
	}
	if resp["status"] != string(OperationPending) {
		t.Errorf("expected status '%s', got %v", OperationPending, resp["status"])
	}
}

func TestTriggerScheduledTask_PublishesTheQueuedEventForTheOtherGateways(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	brk := h.core.Services().Broker()
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(sub, a2a.UserEventTopic("test-ns")); err != nil {
		t.Fatal(err)
	}
	userID := "sam_dev_user"
	taskID := idgen.New()
	if err = h.repo.Create(t.Context(), &store.ScheduledTask{
		ID: taskID, Name: "Trigger Me", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/trigger", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, sub)
	if err != nil {
		t.Fatalf("no user event published: %v", err)
	}
	var evt a2a.UserEvent
	if err := json.Unmarshal(msg.Payload, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Event != "execution_queued" || len(evt.Keys) != 1 || evt.Keys[0] != userID || evt.Origin == "" {
		t.Errorf("user event = %+v, want execution_queued for %s with an origin", evt, userID)
	}
}

func TestTriggerScheduledTask_NotFound(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/00000000-0000-0000-0000-000000000000/trigger", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTriggerScheduledTask_Deleted(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Deleted Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	if err := h.repo.SoftDelete(t.Context(), taskID, userID); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/trigger", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTriggerScheduledTask_DisabledTaskSucceeds(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Disabled Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: false, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/trigger", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestExecutionToDTO_LegacyRowDefaultsToScheduled(t *testing.T) {
	e := &store.ScheduledTaskExecution{
		ID:              tid("exec-legacy"),
		ScheduledTaskID: tid("st-legacy"),
		Status:          "completed",
		TriggerType:     "",
	}
	dto := executionToDTO(e)
	if dto.TriggerType != "scheduled" {
		t.Errorf("trigger_type = %q, want %q (legacy fallback)", dto.TriggerType, "scheduled")
	}
}

func TestExecutionToDTO_PassesThroughManualTrigger(t *testing.T) {
	user := "u-manual"
	e := &store.ScheduledTaskExecution{
		ID:              tid("exec-manual"),
		ScheduledTaskID: tid("st-manual"),
		Status:          "completed",
		TriggerType:     store.TriggerTypeManual,
		TriggeredBy:     &user,
	}
	dto := executionToDTO(e)
	if dto.TriggerType != "manual" {
		t.Errorf("trigger_type = %q, want %q", dto.TriggerType, "manual")
	}
	if dto.TriggeredBy == nil || *dto.TriggeredBy != user {
		t.Errorf("triggered_by = %v, want %q", dto.TriggeredBy, user)
	}

	blob, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(blob, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["triggerType"] != "manual" {
		t.Errorf("wire trigger_type = %v, want 'manual'", wire["triggerType"])
	}
	if wire["triggeredBy"] != user {
		t.Errorf("wire triggered_by = %v, want %q", wire["triggeredBy"], user)
	}
}
func TestRunScheduledTask_NotFound(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/00000000-0000-0000-0000-000000000000/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunScheduledTask_DisabledTaskSucceeds(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Disabled Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: false, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["success"] != true {
		t.Errorf("success = %v, want true", resp["success"])
	}
	if resp["taskId"] != taskID {
		t.Errorf("task_id = %v, want %q", resp["taskId"], taskID)
	}
	if resp["message"] != "Task triggered" {
		t.Errorf("message = %v, want \"Task triggered\"", resp["message"])
	}
}

func TestRunScheduledTask_AlreadyRunning_Returns409(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Busy Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	if err := h.repo.CreateExecution(t.Context(), &store.ScheduledTaskExecution{
		ID: idgen.New(), ScheduledTaskID: taskID, Status: "running", ScheduledFor: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !bytes.Contains([]byte(body), []byte("currently executing")) {
		t.Errorf("response body missing '409 currently executing' message: %s", body)
	}
}

func TestRunScheduledTask_SpentOwnerBudgetOnALegacyTaskNamesTheOwner(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	const owner = "legacy-owner"
	budgettest.BlockUsers(t, h.core.BudgetGate(), time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC), budget.UserKey("", owner))

	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Legacy Task", Namespace: "test-ns", CreatedBy: owner,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	for _, verb := range []string{"run", "trigger"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/"+verb, nil))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: expected 429, got %d: %s", verb, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, "This task can't run now: the usage budget is used up until 1 November 2099 00:00 UTC for this task's owner") {
			t.Errorf("%s: body = %s, want the owner's spent budget, not the caller's", verb, body)
		}
	}
}

func TestRunScheduledTask_SpentBudget_Returns429(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)
	budgettest.BlockMesh(t, h.core.BudgetGate(), time.Date(2099, 11, 1, 0, 0, 0, 0, time.UTC))

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Blocked Task", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	for _, verb := range []string{"run", "trigger"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/"+verb, nil))
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: expected 429, got %d: %s", verb, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, "This task can't run now: this deployment's usage budget is used up until 1 November 2099 00:00 UTC") {
			t.Errorf("%s: body = %s, want the spent budget and its reset time", verb, body)
		}
	}
	execs, err := h.repo.FindExecutionsByTask(t.Context(), taskID, 10, 0, nil, nil)
	if err != nil || len(execs) != 0 {
		t.Errorf("executions = %d, %v; want none recorded for a refused run", len(execs), err)
	}
}

func TestCreateScheduledTask_WhitespaceStripped(t *testing.T) {
	_, mux := setupScheduledTaskTest(t)

	desc := "  has whitespace  "
	body := map[string]any{
		"name":               "  Daily Report  ",
		"description":        desc,
		"scheduleType":       "cron",
		"scheduleExpression": "0 9 * * *",
		"targetAgentName":    "test-agent",
		"taskMessage":        []map[string]string{{"type": "text", "text": "hello"}},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks", jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.Name != "Daily Report" {
		t.Errorf("name = %q, want \"Daily Report\"", resp.Name)
	}
	if resp.Description == nil || *resp.Description != "has whitespace" {
		t.Errorf("description = %v, want \"has whitespace\"", resp.Description)
	}
}

func TestGetScheduledTask_IncludesLastExecution(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "With History", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	older := &store.ScheduledTaskExecution{
		ID: tid("exec-older"), ScheduledTaskID: taskID, Status: "completed",
		ScheduledFor: 100, TriggerType: store.TriggerTypeScheduled,
	}
	newer := &store.ScheduledTaskExecution{
		ID: tid("exec-newer"), ScheduledTaskID: taskID, Status: "failed",
		ScheduledFor: 200, TriggerType: store.TriggerTypeManual,
	}
	for _, e := range []*store.ScheduledTaskExecution{older, newer} {
		if err := h.repo.CreateExecution(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks/"+taskID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.LastExecution == nil {
		t.Fatal("last_execution missing from response")
	}
	if resp.LastExecution.ID != tid("exec-newer") {
		t.Errorf("last_execution.id = %q, want exec-newer", resp.LastExecution.ID)
	}
	if resp.LastExecution.Status != "failed" {
		t.Errorf("last_execution.status = %q, want failed", resp.LastExecution.Status)
	}
	if resp.LastExecution.TriggerType != "manual" {
		t.Errorf("last_execution.trigger_type = %q, want manual", resp.LastExecution.TriggerType)
	}
}

func TestUpdateScheduledTask_WhitespaceStripped(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	desc := "original"
	task := &store.ScheduledTask{
		ID: taskID, Name: "Original", Description: &desc, Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"name":        "  Updated Name  ",
		"description": "   new desc   ",
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, jsonBody(t, body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.Name != "Updated Name" {
		t.Errorf("name = %q, want \"Updated Name\"", resp.Name)
	}
	if resp.Description == nil || *resp.Description != "new desc" {
		t.Errorf("description = %v, want \"new desc\"", resp.Description)
	}
}

func TestUpdateScheduledTask_NullDescriptionCleared(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	desc := "to be cleared"
	task := &store.ScheduledTask{
		ID: taskID, Name: "Clearable", Description: &desc, Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"description": null}`)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PATCH", "/api/v1/scheduledTasks/"+taskID, bytes.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResp[scheduledTaskResponse](t, rec)
	if resp.Description != nil {
		t.Errorf("description = %v, want nil after explicit null", *resp.Description)
	}
}

func TestExecutionToLastSummary_NilInput(t *testing.T) {
	if got := executionToLastSummary(nil); got != nil {
		t.Errorf("executionToLastSummary(nil) = %v, want nil", got)
	}
}

func TestExecutionToLastSummary_DurationFromTimestamps(t *testing.T) {
	started := int64(1000)
	completed := int64(1450)
	exec := &store.ScheduledTaskExecution{
		ID:           tid("exec-dur"),
		Status:       "completed",
		ScheduledFor: 100,
		StartedAt:    &started,
		CompletedAt:  &completed,
		TriggerType:  store.TriggerTypeManual,
	}
	got := executionToLastSummary(exec)
	if got.DurationMs == nil {
		t.Fatal("DurationMs nil; expected computed value")
	}
	if *got.DurationMs != 450 {
		t.Errorf("DurationMs = %d, want 450", *got.DurationMs)
	}
	if got.TriggerType != "manual" {
		t.Errorf("trigger_type = %q, want manual", got.TriggerType)
	}
}

func TestListScheduledTasks_IncludesLastExecution(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	userID := "sam_dev_user"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "List With History", Namespace: "test-ns",
		UserID: &userID, CreatedBy: userID,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hi"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	older := &store.ScheduledTaskExecution{
		ID: tid("exec-list-older"), ScheduledTaskID: taskID, Status: "completed",
		ScheduledFor: 100, TriggerType: store.TriggerTypeScheduled,
	}
	newer := &store.ScheduledTaskExecution{
		ID: tid("exec-list-newer"), ScheduledTaskID: taskID, Status: "failed",
		ScheduledFor: 200, TriggerType: store.TriggerTypeManual,
	}
	for _, e := range []*store.ScheduledTaskExecution{older, newer} {
		if err := h.repo.CreateExecution(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/scheduledTasks", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp PaginatedResponse[scheduledTaskResponse]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
	got := resp.Data[0]
	if got.LastExecution == nil {
		t.Fatal("last_execution missing in list response")
	}
	if got.LastExecution.ID != tid("exec-list-newer") {
		t.Errorf("last_execution.id = %q, want exec-list-newer", got.LastExecution.ID)
	}
}

func TestRunScheduledTask_OtherUserGetsNotFound(t *testing.T) {
	h, mux := setupScheduledTaskTest(t)

	otherOwner := "someone-else"
	taskID := idgen.New()
	task := &store.ScheduledTask{
		ID: taskID, Name: "Other Owner", Namespace: "test-ns",
		UserID: &otherOwner, CreatedBy: otherOwner,
		ScheduleType: "cron", ScheduleExpression: "0 9 * * *",
		Timezone: "UTC", TargetAgentName: "agent", TargetType: "agent",
		TaskMessage: `[{"type":"text","text":"hello"}]`, Enabled: true, Source: "ui",
		TimeoutSeconds: 30,
	}
	if err := h.repo.Create(t.Context(), task); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/scheduledTasks/"+taskID+"/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAttachSessionIDs(t *testing.T) {
	h, _ := setupScheduledTaskTest(t)
	ctx := context.Background()

	mappedExec := idgen.New()
	unmappedExec := idgen.New()

	sessID, err := h.sessKeyRepo.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeScheduled,
		ExternalKey: mappedExec,
		UserID:      "u1",
	})
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	dtos := []executionResponse{
		{ID: mappedExec},
		{ID: unmappedExec},
	}
	h.attachSessionIDs(ctx, []*executionResponse{&dtos[0], &dtos[1]})

	if dtos[0].SessionID == nil || *dtos[0].SessionID != sessID {
		t.Errorf("mapped execution: SessionID = %v, want %q", dtos[0].SessionID, sessID)
	}
	if dtos[1].SessionID != nil {
		t.Errorf("unmapped execution: SessionID = %v, want nil", dtos[1].SessionID)
	}
}

func TestAttachSessionIDs_ListsTheSessionOfAQuietRunOnlyWhileItKeptOne(t *testing.T) {
	h, _ := setupScheduledTaskTest(t)
	ctx := context.Background()
	keptExec, hiddenExec := idgen.New(), idgen.New()
	keptSession, err := h.sessKeyRepo.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeScheduled,
		ExternalKey: keptExec,
		UserID:      "u1",
	})
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	kept := executionResponse{ID: keptExec, Status: "quiet"}
	hidden := executionResponse{ID: hiddenExec, Status: "quiet"}

	h.attachSessionIDs(ctx, []*executionResponse{&kept, &hidden})

	if diff := cmp.Diff(&keptSession, kept.SessionID); diff != "" {
		t.Errorf("quiet run that kept its session: SessionID mismatch (-want +got):\n%s", diff)
	}
	if hidden.SessionID != nil {
		t.Errorf("quiet run whose session was hidden: SessionID = %q, want none", *hidden.SessionID)
	}
}

func TestAttachSessionIDs_EmptyAndNilGuards(t *testing.T) {
	h, _ := setupScheduledTaskTest(t)

	h.attachSessionIDs(context.Background(), nil)
	h.attachSessionIDs(context.Background(), []*executionResponse{})

	h2 := &scheduledTaskHandler{logger: slog.Default()}
	dto := executionResponse{ID: idgen.New()}
	h2.attachSessionIDs(context.Background(), []*executionResponse{&dto})
	if dto.SessionID != nil {
		t.Errorf("expected SessionID to remain nil with no repo, got %v", dto.SessionID)
	}
}

func TestExecutionToLastSummary_DefaultTriggerType(t *testing.T) {
	exec := &store.ScheduledTaskExecution{
		ID:           tid("exec-1"),
		Status:       "completed",
		ScheduledFor: 100,
	}
	got := executionToLastSummary(exec)
	if got.TriggerType != "scheduled" {
		t.Errorf("trigger_type = %q, want scheduled as default", got.TriggerType)
	}
}

func TestDeriveTaskStatus(t *testing.T) {
	tests := []struct {
		name                    string
		enabled                 bool
		consecutiveFailureCount int
		want                    string
	}{
		{"enabled no failures", true, 0, "active"},
		{"enabled with failures", true, 3, "error"},
		{"disabled no failures", false, 0, "paused"},
		{"paused wins over error", false, 3, "paused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveTaskStatus(tt.enabled, tt.consecutiveFailureCount); got != tt.want {
				t.Errorf("deriveTaskStatus(%v, %d) = %q, want %q", tt.enabled, tt.consecutiveFailureCount, got, tt.want)
			}
		})
	}
}

func TestRequireRegisteredAgent_ResolvesFriendlyName(t *testing.T) {
	_, core, _ := setupTestAdapter(t)
	const slug = "agent_019ea471_5051_73c7_893a_7679460fd074"
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{
		Name: slug,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": "Orchestrator"},
			}},
		},
	})

	if err := requireRegisteredAgent(core, "Orchestrator", "agent"); err != nil {
		t.Errorf("friendly/display name must be accepted, got %v", err)
	}
	if err := requireRegisteredAgent(core, slug, "agent"); err != nil {
		t.Errorf("canonical card name must still be accepted, got %v", err)
	}
	if err := requireRegisteredAgent(core, "Ghost", "agent"); err == nil {
		t.Error("an unknown agent name must be rejected")
	}
	if err := requireRegisteredAgent(core, "anything", "workflow"); err != nil {
		t.Errorf("workflow targets must skip the agent check, got %v", err)
	}
}
