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

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	sweepOwner    = "sweep-owner@example.com"
	sweepStranger = "sweep-stranger@example.com"
	sweepAuditor  = "sweep-auditor@example.com"
	sweepAgent    = "sweep-agent"
	sweepSession  = "01a0af5b-5eed-7000-8000-5eed5eed5eed"
	sweepCanary   = "CANARY-cross-user-task-content"
)

type taskSurfaceRoute struct {
	name            string
	registryPath    string
	method          string
	request         func(taskID string) *http.Request
	wantOwner       int
	wantRefused     int
	wantActivity    int
	wantAgentTrace  int
	streams         bool
	wantOwnerBody   string
	wantRefusedBody string
}

func assertRefusedBody(t *testing.T, rt taskSurfaceRoute, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rt.wantRefusedBody != "" && !strings.Contains(rec.Body.String(), rt.wantRefusedBody) {
		t.Errorf("refused by the wrong check: body does not contain %q; body=%s", rt.wantRefusedBody, rec.Body.String())
	}
}

func taskSurfaceRoutes() []taskSurfaceRoute {
	get := func(path string) func(string) *http.Request {
		return func(taskID string) *http.Request {
			return httptest.NewRequest(http.MethodGet, strings.ReplaceAll(path, "{}", taskID), nil)
		}
	}
	return []taskSurfaceRoute{
		{
			name:           "status",
			registryPath:   "/api/v1/tasks/{id}/status",
			method:         http.MethodGet,
			request:        get("/api/v1/tasks/{}/status"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusOK,
			wantOwnerBody:  `"isRunning"`,
		},
		{
			name:           "events",
			registryPath:   "/api/v1/tasks/{id}/events",
			method:         http.MethodGet,
			request:        get("/api/v1/tasks/{}/events"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusOK,
			wantOwnerBody:  sweepCanary,
		},
		{
			name:           "bufferedEvents",
			registryPath:   "/api/v1/tasks/{id}/events/buffered",
			method:         http.MethodGet,
			request:        get("/api/v1/tasks/{}/events/buffered"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  sweepCanary,
		},
		{
			name:         "deleteBufferedEvents",
			registryPath: "/api/v1/tasks/{id}/events/buffered",
			method:       http.MethodDelete,
			request: func(taskID string) *http.Request {
				return httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+taskID+"/events/buffered", nil)
			},
			wantOwner:      http.StatusNoContent,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusNotFound,
			wantAgentTrace: http.StatusNotFound,
		},
		{
			name:           "titleData",
			registryPath:   "/api/v1/tasks/{id}/titleData",
			method:         http.MethodGet,
			request:        get("/api/v1/tasks/{}/titleData"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  sweepCanary,
		},
		{
			name:           "getTask",
			registryPath:   "/api/v1/tasks/{id}",
			method:         http.MethodGet,
			request:        get("/api/v1/tasks/{}"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusOK,
			wantOwnerBody:  "invocation_details",
		},
		{
			name:           "chatV2GetTask",
			registryPath:   chatv2.Prefix + "/tasks/{id}",
			method:         http.MethodGet,
			request:        get(chatv2.Prefix + "/tasks/{}"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  `"type":"task"`,
		},
		{
			name:           "chatV2ExportTask",
			registryPath:   chatv2.Prefix + "/tasks/{id}/export",
			method:         http.MethodGet,
			request:        get(chatv2.Prefix + "/tasks/{}/export"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusOK,
			wantOwnerBody:  "invocation_details",
		},
		{
			name:         "chatV2CancelTask",
			registryPath: chatv2.Prefix + "/tasks/{id}/cancel",
			method:       http.MethodPut,
			request: func(taskID string) *http.Request {
				return httptest.NewRequest(http.MethodPut, chatv2.Prefix+"/tasks/"+taskID+"/cancel", nil)
			},
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusNotFound,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  `"type":"task"`,
		},
		{
			name:           "chatV2Events",
			registryPath:   chatv2.Prefix + "/tasks/{id}/events",
			method:         http.MethodGet,
			request:        get(chatv2.Prefix + "/tasks/{}/events"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  "SSE connection established",
			streams:        true,
		},
		{
			name:           "chatV2PendingInput",
			registryPath:   chatv2.Prefix + "/tasks/{id}/pendingInput",
			method:         http.MethodGet,
			request:        get(chatv2.Prefix + "/tasks/{}/pendingInput"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNoContent,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNoContent,
			wantOwnerBody:  sweepCanary,
		},
		{
			name:         "chatV2AnswerInput",
			registryPath: chatv2.Prefix + "/tasks/{id}/userInput",
			method:       http.MethodPut,
			request: func(taskID string) *http.Request {
				body := `{"agentName":"` + sweepAgent + `","requestId":"sweep-req","status":"answered","data":{"x":1}}`
				req := httptest.NewRequest(http.MethodPut, chatv2.Prefix+"/tasks/"+taskID+"/userInput", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusNotFound,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  `"type":"userInputAnswer"`,
		},
		{
			name:           "sseSubscribe",
			registryPath:   "/api/v1/sse/subscribe/{taskId}",
			method:         http.MethodGet,
			request:        get("/api/v1/sse/subscribe/{}"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  "SSE connection established",
			streams:        true,
		},
		{
			name:           "pendingInput",
			registryPath:   "/api/v1/tasks/{taskId}/pendingInput",
			method:         http.MethodGet,
			request:        get("/api/v1/tasks/{}/pendingInput"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNoContent,
			wantActivity:   http.StatusOK,
			wantAgentTrace: http.StatusNoContent,
			wantOwnerBody:  sweepCanary,
		},
		{
			name:         "cancel",
			registryPath: "/api/v1/tasks/",
			method:       http.MethodPost,
			request: func(taskID string) *http.Request {
				return httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+":cancel", nil)
			},
			wantOwner:      http.StatusAccepted,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusNotFound,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  `"status":"inProgress"`,
		},
		{
			name:           "executionByA2aTask",
			registryPath:   "/api/v1/scheduledTasks/executions/byA2aTask/{a2aTaskId}",
			method:         http.MethodGet,
			request:        get("/api/v1/scheduledTasks/executions/byA2aTask/{}"),
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusNotFound,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  `"scheduledTaskId"`,
		},
		{
			name:         "userInputRespond",
			registryPath: "/api/v1/userInput/respond",
			method:       http.MethodPost,
			request: func(taskID string) *http.Request {
				body := `{"taskId":"` + taskID + `","agentName":"` + sweepAgent +
					`","requestId":"sweep-req","status":"answered","data":{"x":1}}`
				req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			wantOwner:      http.StatusOK,
			wantRefused:    http.StatusNotFound,
			wantActivity:   http.StatusNotFound,
			wantAgentTrace: http.StatusNotFound,
			wantOwnerBody:  `"status":"ok"`,
		},
		{
			name:         "feedbackSubmit",
			registryPath: "/api/v1/feedback",
			method:       http.MethodPost,
			request: func(taskID string) *http.Request {
				body := `{"taskId":"` + taskID + `","sessionId":"` + sweepSession + `","feedbackType":"up"}`
				req := httptest.NewRequest(http.MethodPost, "/api/v1/feedback", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			wantOwner:       http.StatusOK,
			wantRefused:     http.StatusNotFound,
			wantActivity:    http.StatusNotFound,
			wantAgentTrace:  http.StatusNotFound,
			wantOwnerBody:   `"status":"received"`,
			wantRefusedBody: taskNotFoundMsg,
		},
	}
}

func seedSweepTask(t *testing.T, a *HTTPSSEAdapter) string {
	t.Helper()
	ctx := context.Background()
	taskID := idgen.New()
	status := "working"

	repo := store.NewTaskRepo(a.core.Store())
	initial := sweepCanary
	handlerName, handlerType := sweepAgent, store.TaskHandlerTypeAgent
	if err := repo.Create(ctx, &store.Task{
		ID:                 taskID,
		UserID:             sweepOwner,
		StartTime:          1000,
		Status:             &status,
		ExecutionMode:      "foreground",
		Kind:               store.TaskKindNormal,
		InitialRequestText: &initial,
		HandlerName:        &handlerName,
		HandlerType:        &handlerType,
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := store.NewSessionRepo(a.core.Store()).Create(ctx, &store.Session{ID: sweepSession, UserID: sweepOwner}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_ = a.core.Tasks().Register(taskID, sweepAgent, sweepOwner, entrypoint.TaskIdentity{SessionID: sweepSession})
	a.core.Tasks().SetWaiting(taskID, "sweep-req")
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: sweepAgent, URL: "solace:test"})
	a.sseManager.RegisterTask(taskID, sweepSession, sweepOwner)

	schedRepo := store.NewScheduledTaskRepo(a.core.Store())
	owner := sweepOwner
	sched := &store.ScheduledTask{
		ID:                 idgen.New(),
		Name:               "sweep-" + idgen.New(),
		Namespace:          "test-ns",
		UserID:             &owner,
		CreatedBy:          sweepOwner,
		ScheduleType:       "cron",
		ScheduleExpression: "0 0 * * *",
		Timezone:           "UTC",
		TargetAgentName:    sweepAgent,
		TargetType:         "agent",
		TaskMessage:        `[{"kind":"text","text":"` + sweepCanary + `"}]`,
		Enabled:            true,
	}
	if err := schedRepo.Create(ctx, sched); err != nil {
		t.Fatalf("create scheduled task: %v", err)
	}
	snapshot := `{"promptText":"` + sweepCanary + `"}`
	if err := schedRepo.CreateExecution(ctx, &store.ScheduledTaskExecution{
		ID:              idgen.New(),
		ScheduledTaskID: sched.ID,
		Status:          "completed",
		A2ATaskID:       &taskID,
		ScheduledFor:    1000,
		TaskSnapshot:    &snapshot,
	}); err != nil {
		t.Fatalf("create scheduled execution: %v", err)
	}

	if a.eventLog != nil {
		pending := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":` +
			`{"type":"user_input_request","request_id":"sweep-req","surface":{"secret":"` + sweepCanary + `"}}}]}}}}`)
		if _, err := a.eventLog.Append(ctx, taskID, sweepSession, sweepOwner, "status_update", pending); err != nil {
			t.Fatalf("seed event log: %v", err)
		}
		waitForSeededBufferRow(t, a, taskID)
	}
	return taskID
}

func waitForSeededBufferRow(t *testing.T, a *HTTPSSEAdapter, taskID string) {
	t.Helper()

	repo := store.NewSSEEventRepo(a.core.Store())
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := repo.GetByTask(context.Background(), taskID, 0)
		if err != nil {
			t.Fatalf("wait for seeded buffer row: %v", err)
		}
		if len(rows) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("seeded event never reached sse_event_buffer — the canary the sweep asserts on would be missing for reasons unrelated to authorization")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requireServedBody(t *testing.T, rec *httptest.ResponseRecorder, rt taskSurfaceRoute, who string) {
	t.Helper()
	if rec.Code != http.StatusOK || rt.wantOwnerBody == "" {
		return
	}
	if !strings.Contains(rec.Body.String(), rt.wantOwnerBody) {
		t.Errorf("%s got 200 but the body does not contain %q — served but empty; body=%s", who, rt.wantOwnerBody, rec.Body.String())
	}
}

func serveAs(t *testing.T, a *HTTPSSEAdapter, rt taskSurfaceRoute, taskID, caller string, scopes ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := withIdentity(rt.request(taskID), caller, scopes...)
	if rt.streams {
		return serveUntilFirstWrite(t, a, req)
	}
	rec := httptest.NewRecorder()
	a.Mux().ServeHTTP(rec, req)
	return rec
}

func serveUntilFirstWrite(t *testing.T, a *HTTPSSEAdapter, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ctx, stop := context.WithTimeout(req.Context(), 10*time.Second)
	defer stop()
	rec := &firstWriteRecorder{ResponseRecorder: httptest.NewRecorder(), stop: stop}
	a.Mux().ServeHTTP(rec, req.WithContext(ctx))
	return rec.ResponseRecorder
}

type firstWriteRecorder struct {
	*httptest.ResponseRecorder
	stop context.CancelFunc
}

func (r *firstWriteRecorder) WriteHeader(code int) {
	r.ResponseRecorder.WriteHeader(code)
	r.stop()
}

func (r *firstWriteRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(b)
	r.stop()
	return n, err
}

func TestTaskSurface_CrossUserSweep(t *testing.T) {
	for _, rt := range taskSurfaceRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			adapter, _, _, _, _ := setupTestAdapterWithDB(t)
			taskID := seedSweepTask(t, adapter)

			t.Run("owner is served", func(t *testing.T) {
				rec := serveAs(t, adapter, rt, taskID, sweepOwner)
				if rec.Code != rt.wantOwner {
					t.Fatalf("owner got %d, want %d — the ownership check refused the owner; body=%s",
						rec.Code, rt.wantOwner, rec.Body.String())
				}
				if rt.wantOwnerBody != "" && !strings.Contains(rec.Body.String(), rt.wantOwnerBody) {
					t.Errorf("owner got %d but the body does not contain %q — the handler may have written nothing; body=%s",
						rec.Code, rt.wantOwnerBody, rec.Body.String())
				}
			})

			t.Run("stranger is refused and sees nothing", func(t *testing.T) {
				rec := serveAs(t, adapter, rt, taskID, sweepStranger)
				if rec.Code != rt.wantRefused {
					t.Errorf("stranger got %d, want %d; body=%s", rec.Code, rt.wantRefused, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), sweepCanary) {
					t.Errorf("stranger received the owner's task content: %s", rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), "SSE connection established") {
					t.Error("stranger was attached to the owner's event stream")
				}
				assertRefusedBody(t, rt, rec)
			})

			t.Run("activity scope gets its declared access", func(t *testing.T) {
				rec := serveAs(t, adapter, rt, taskID, sweepAuditor, rbac.ScopeActivityRead)
				if rec.Code != rt.wantActivity {
					t.Errorf("activity:*:read holder got %d, want %d; body=%s",
						rec.Code, rt.wantActivity, rec.Body.String())
				}
				if rt.wantActivity == rt.wantRefused && strings.Contains(rec.Body.String(), sweepCanary) {
					t.Errorf("activity:*:read holder was refused but still received task content: %s", rec.Body.String())
				}
				if rt.wantActivity == rt.wantRefused {
					assertRefusedBody(t, rt, rec)
				}
				requireServedBody(t, rec, rt, "activity:*:read holder")
			})

			t.Run("a trace grant on the root agent gets its declared access", func(t *testing.T) {
				rec := serveAs(t, adapter, rt, taskID, sweepAuditor, rbac.AgentReadTraceScope(sweepAgent))
				if rec.Code != rt.wantAgentTrace {
					t.Errorf("agent:%s:read_trace holder got %d, want %d; body=%s",
						sweepAgent, rec.Code, rt.wantAgentTrace, rec.Body.String())
				}
				if rt.wantAgentTrace == rt.wantRefused && strings.Contains(rec.Body.String(), sweepCanary) {
					t.Errorf("trace holder was refused but still received task content: %s", rec.Body.String())
				}
				if rt.wantAgentTrace == rt.wantRefused {
					assertRefusedBody(t, rt, rec)
				}
				requireServedBody(t, rec, rt, "trace holder")
			})

			t.Run("a trace grant on some other agent is not elevated", func(t *testing.T) {
				rec := serveAs(t, adapter, rt, taskID, sweepStranger, rbac.AgentReadTraceScope("some-other-agent"))
				if rec.Code != rt.wantRefused {
					t.Errorf("caller holding a trace grant on another agent got %d, want %d", rec.Code, rt.wantRefused)
				}
				if strings.Contains(rec.Body.String(), sweepCanary) {
					t.Errorf("caller holding a trace grant on another agent received the owner's task content: %s", rec.Body.String())
				}
				assertRefusedBody(t, rt, rec)
			})

			t.Run("an unrelated scope is not elevated", func(t *testing.T) {
				rec := serveAs(t, adapter, rt, taskID, sweepStranger, rbac.ScopeAgentInvokeAny)
				if rec.Code != rt.wantRefused {
					t.Errorf("caller holding only %s got %d, want %d", rbac.ScopeAgentInvokeAny, rec.Code, rt.wantRefused)
				}
				if strings.Contains(rec.Body.String(), sweepCanary) {
					t.Errorf("caller holding only %s received the owner's task content: %s",
						rbac.ScopeAgentInvokeAny, rec.Body.String())
				}
				assertRefusedBody(t, rt, rec)
			})
		})
	}
}

func TestTaskSurface_SweepIsExhaustive(t *testing.T) {
	covered := make(map[string]bool)
	for _, rt := range taskSurfaceRoutes() {
		covered[rt.method+" "+rt.registryPath] = true
	}

	adapter := newCoverageAdapter(t)
	for _, r := range adapter.Routes() {
		if !addressesOneTask(r.Method, r.Path) {
			continue
		}
		if !covered[r.Method+" "+r.Path] {
			t.Errorf("route %s %s addresses a single task but is not in taskSurfaceRoutes() — add it with the status an owner, a stranger and an activity:*:read holder should each receive",
				r.Method, r.Path)
		}
	}

	registered := make(map[string]bool)
	for _, r := range adapter.Routes() {
		registered[r.Method+" "+r.Path] = true
	}
	for key := range covered {
		if !registered[key] {
			t.Errorf("taskSurfaceRoutes() covers %q, which is not registered — the sweep is asserting against a mux miss", key)
		}
	}
}

func addressesOneTask(method, path string) bool {
	switch path {
	case "/api/v1/userInput/respond":
		return true
	case "/api/v1/tasks/":
		return true
	case "/api/v1/feedback":
		return method == http.MethodPost
	}
	if strings.Contains(path, "{taskId}") || strings.Contains(path, "{a2aTaskId}") {
		return true
	}
	if strings.HasPrefix(path, chatv2.Prefix+"/tasks/") && strings.Contains(path, "{id}") {
		return true
	}
	return strings.HasPrefix(path, "/api/v1/tasks/") && strings.Contains(path, "{id}")
}
