// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newHILHandler(t *testing.T) (*hilHandler, *http.ServeMux, eventlog.TaskEventLog) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	memLog := eventlog.NewMemoryEventLog()
	h := &hilHandler{core: core, eventLog: memLog, logger: adapter.logger}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/userInput/respond", h.handleRespond)
	mux.HandleFunc("GET /api/v1/tasks/{taskId}/pendingInput", h.handlePendingInput)
	return h, mux, memLog
}

func respondBody(taskID, agentName, requestID string) string {
	return fmt.Sprintf(`{"taskId":%q,"agentName":%q,"requestId":%q,"status":"answered","data":{}}`, taskID, agentName, requestID)
}

func TestHandleRespond_RejectsMalformedBody(t *testing.T) {
	_, mux, _ := newHILHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for malformed body", rec.Code)
	}
}

func TestHandleRespond_RejectsMissingRequiredFields(t *testing.T) {
	_, mux, _ := newHILHandler(t)

	cases := []struct {
		name string
		body string
	}{
		{"missing taskId", `{"agentName":"a","requestId":"r","status":"answered","data":{}}`},
		{"missing agentName", `{"taskId":"t","requestId":"r","status":"answered","data":{}}`},
		{"missing requestId", `{"taskId":"t","agentName":"a","status":"answered","data":{}}`},
		{"missing status", `{"taskId":"t","agentName":"a","requestId":"r","data":{}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (%s)", rec.Code, c.name)
			}
		})
	}
}

func TestHandleRespond_HappyPathEndsTheWaitAndPublishesMarker(t *testing.T) {
	h, mux, _ := newHILHandler(t)

	brk := h.core.Services().Broker()
	q, err := brk.CreateQueue("test-log-records", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.GatewayLogSubscription("test-ns", "test-gw")); err != nil {
		t.Fatal(err)
	}

	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent"})
	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "sam_dev_user", entrypoint.TaskIdentity{SessionID: "sess-1"})
	h.core.Tasks().SetWaiting(taskID, "req-7")

	body := fmt.Sprintf(`{"taskId":%q,"agentName":"my-agent","requestId":"req-7","status":"answered","data":{"foo":"bar"}}`, taskID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if id, recorded := h.core.PendingUserInput(taskID); id != "" || !recorded {
		t.Errorf("the answer must end the wait on the record: %q %v", id, recorded)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("no marker published on the log topic: %v", err)
	}
	if msg.Topic != a2a.GatewayLogTopic("test-ns", "test-gw", taskID) {
		t.Errorf("topic = %q", msg.Topic)
	}
	var env struct {
		Result a2a.TaskStatusUpdateEvent `json:"result"`
	}
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.Kind != "status-update" || env.Result.TaskID != taskID || env.Result.ContextID != "sess-1" {
		t.Errorf("marker envelope = %+v", env.Result)
	}
	data := extractDataPartFromPayload(msg.Payload, "user_input_response")
	if data == nil || data["request_id"] != "req-7" || data["status"] != "answered" {
		t.Errorf("marker data = %v", data)
	}
}

func TestHandleRespond_HappyPathWithoutEventLog(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	h := &hilHandler{core: core, eventLog: nil, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/userInput/respond", h.handleRespond)

	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "agent-no-log"})
	taskID := idgen.New()
	_ = core.Tasks().Register(taskID, "agent-no-log", "sam_dev_user", entrypoint.TaskIdentity{})

	body := respondBody(taskID, "agent-no-log", "r")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 when eventLog is nil", rec.Code)
	}
}

func TestHandleRespond_UnknownAgentReturns404(t *testing.T) {
	_, mux, _ := newHILHandler(t)

	body := `{"taskId":"t","agentName":"nope","requestId":"r","status":"answered","data":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unknown agent", rec.Code)
	}
}

func TestHandlePendingInput_MissingTaskIDReturnsBadRequest(t *testing.T) {
	_, _, _ = newHILHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks//pendingInput", nil)
	h := &hilHandler{}
	h.handlePendingInput(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for missing taskId", rec.Code)
	}
}

func TestHandlePendingInput_NotRunningReturns204(t *testing.T) {
	_, mux, _ := newHILHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+idgen.New()+"/pendingInput", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 for unregistered task", rec.Code)
	}
}

func TestHandlePendingInput_MalformedTaskIDReturns400(t *testing.T) {
	_, mux, _ := newHILHandler(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/not-a-uuid/pendingInput", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a task id that is not a UUID", rec.Code)
	}
}

func TestHandlePendingInput_NoMatchingEventReturns204(t *testing.T) {
	h, mux, _ := newHILHandler(t)

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "agent-x", "u@example.com", entrypoint.TaskIdentity{})
	h.core.Tasks().SetWaiting(taskID, "req-1")

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 with no pending request", rec.Code)
	}
}

func TestHandlePendingInput_ReturnsPendingRequest(t *testing.T) {
	h, mux, memLog := newHILHandler(t)

	taskID := idgen.New()
	agentName := "my-agent"
	_ = h.core.Tasks().Register(taskID, agentName, "u@example.com", entrypoint.TaskIdentity{})
	h.core.Tasks().SetWaiting(taskID, "req-99")

	rawPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-99","surface":{"foo":"bar"}}}]}}}}`)
	if _, err := memLog.Append(context.Background(), taskID, "", "", "status_update", rawPayload); err != nil {
		t.Fatalf("Append: %v", err)
	}

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"taskId":"`+taskID+`"`) {
		t.Errorf("response missing taskId: %s", rec.Body.String())
	}
	if !contains(rec.Body.String(), `"agentName":"`+agentName+`"`) {
		t.Errorf("response missing agentName: %s", rec.Body.String())
	}
	if !contains(rec.Body.String(), `"request_id":"req-99"`) {
		t.Errorf("response missing request data: %s", rec.Body.String())
	}
}

func TestHandlePendingInput_SuppressesAnAnsweredRequest(t *testing.T) {
	h, mux, memLog := newHILHandler(t)

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "u@example.com", entrypoint.TaskIdentity{})
	reqPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-1"}}]}}}}`)
	if _, err := memLog.Append(t.Context(), taskID, "", "", "status_update", reqPayload); err != nil {
		t.Fatalf("Append req: %v", err)
	}
	h.core.Tasks().SetWaiting(taskID, "req-1")
	h.core.Tasks().ClearWaiting(taskID, "req-1")

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (an answered question must not be shown again)", rec.Code)
	}
}

func TestHandlePendingInput_RowFromThePreviousReleaseReadsTheEventLog(t *testing.T) {
	h, mux, memLog := newHILHandler(t)

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "u@example.com", entrypoint.TaskIdentity{})
	if _, err := h.core.Store().ExecContext(t.Context(), `UPDATE task_records SET waiting_request_id = NULL WHERE task_id = ?`, taskID); err != nil {
		t.Fatal(err)
	}
	reqPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-1"}}]}}}}`)
	if _, err := memLog.Append(t.Context(), taskID, "", "", "status_update", reqPayload); err != nil {
		t.Fatalf("Append req: %v", err)
	}

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: an open question from before the upgrade must still be shown, body=%s", rec.Code, rec.Body.String())
	}

	respPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_response","request_id":"req-1","status":"answered"}}]}}}}`)
	if _, err := memLog.Append(t.Context(), taskID, "", "", "status_update", respPayload); err != nil {
		t.Fatalf("Append resp: %v", err)
	}
	rec = httptest.NewRecorder()
	req = withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204: the previous release's marker says the question was answered", rec.Code)
	}
}

func TestHandlePendingInput_LogBehindTheRecordShowsNothing(t *testing.T) {
	h, mux, memLog := newHILHandler(t)

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "u@example.com", entrypoint.TaskIdentity{})
	reqPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-1"}}]}}}}`)
	if _, err := memLog.Append(t.Context(), taskID, "", "", "status_update", reqPayload); err != nil {
		t.Fatalf("Append req: %v", err)
	}
	h.core.Tasks().SetWaiting(taskID, "req-2")

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 while the log still ends with the answered question", rec.Code)
	}
}

func TestHandlePendingInput_NilEventLogReturns204(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	h := &hilHandler{core: core, eventLog: nil, logger: adapter.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tasks/{taskId}/pendingInput", h.handlePendingInput)
	_ = adapter

	taskID := idgen.New()
	_ = core.Tasks().Register(taskID, "agent-x", "u@example.com", entrypoint.TaskIdentity{})

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "u@example.com")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 when eventLog is nil", rec.Code)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestHandlePendingInput_CrossUserGets204(t *testing.T) {
	h, mux, memLog := newHILHandler(t)

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "owner@example.com", entrypoint.TaskIdentity{})
	h.core.Tasks().SetWaiting(taskID, "req-1")

	rawPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-1","surface":{"secret":"do-not-leak"}}}]}}}}`)
	if _, err := memLog.Append(context.Background(), taskID, "", "", "status_update", rawPayload); err != nil {
		t.Fatalf("Append: %v", err)
	}

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil), "intruder@example.com")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("cross-user got %d, want 204", rec.Code)
	}
	if contains(rec.Body.String(), "do-not-leak") {
		t.Errorf("cross-user caller received the pending surface: %s", rec.Body.String())
	}
}

func TestHandlePendingInput_ActivityScopeReadsAnyTask(t *testing.T) {
	h, mux, memLog := newHILHandler(t)

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "owner@example.com", entrypoint.TaskIdentity{})
	h.core.Tasks().SetWaiting(taskID, "req-2")

	rawPayload := []byte(`{"result":{"status":{"message":{"parts":[{"kind":"data","data":{"type":"user_input_request","request_id":"req-2","surface":{"foo":"bar"}}}]}}}}`)
	if _, err := memLog.Append(context.Background(), taskID, "", "", "status_update", rawPayload); err != nil {
		t.Fatalf("Append: %v", err)
	}

	rec := httptest.NewRecorder()
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/pendingInput", nil),
		"auditor@example.com", rbac.ScopeActivityRead)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("activity-scope holder got %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"request_id":"req-2"`) {
		t.Errorf("activity-scope holder did not receive the request: %s", rec.Body.String())
	}
}

func TestHandleRespond_CrossUserCannotAnswer(t *testing.T) {
	h, mux, _ := newHILHandler(t)

	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent", URL: "solace:test"})
	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "victim@example.com", entrypoint.TaskIdentity{})

	body := `{"taskId":"` + taskID + `","agentName":"my-agent","requestId":"req-1","status":"answered","data":{"approved":true}}`
	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body)),
		"intruder@example.com")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-user answer got %d, want 404 — an intruder answered another user's HIL prompt", rec.Code)
	}
}

func TestHandleRespond_UnknownTaskIsRefused(t *testing.T) {
	h, mux, _ := newHILHandler(t)
	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent", URL: "solace:test"})

	body := `{"taskId":"never-existed","agentName":"my-agent","requestId":"req-1","status":"answered","data":{}}`
	rec := httptest.NewRecorder()
	req := withIdentity(
		httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body)),
		"anyone@example.com")
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown task got %d, want 404", rec.Code)
	}
}

func TestHandleRespond_ActivityScopeCannotAnswerAnothersPrompt(t *testing.T) {
	h, mux, _ := newHILHandler(t)
	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent", URL: "solace:test"})

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "owner@example.com", entrypoint.TaskIdentity{})

	body := `{"taskId":"` + taskID + `","agentName":"my-agent","requestId":"req-9","status":"answered","data":{"approve":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "auditor@example.com", rbac.ScopeActivityRead, rbac.ScopeAgentInvokeAny)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("got 403 (delegation denied) — the ownership gate admitted an activity:*:read holder to a write endpoint and only the agent check refused them; body=%s", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("activity:*:read holder with invoke rights got %d, want 404 — a read scope must not authorize writing into another user's task; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRespond_OwnerWithSameScopesIsServed(t *testing.T) {
	h, mux, _ := newHILHandler(t)
	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent", URL: "solace:test"})

	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "owner@example.com", entrypoint.TaskIdentity{})

	body := `{"taskId":"` + taskID + `","agentName":"my-agent","requestId":"req-10","status":"answered","data":{"approve":true}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withIdentity(req, "owner@example.com", rbac.ScopeActivityRead, rbac.ScopeAgentInvokeAny)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("owner got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRespond_MalformedTaskIDDoesNotReachTheDurableLookup(t *testing.T) {
	h, mux, _ := newHILHandler(t)
	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent"})

	if _, err := h.core.Store().Exec("ALTER TABLE tasks RENAME TO tasks_hidden_by_test"); err != nil {
		t.Fatalf("hide tasks table: %v", err)
	}

	respond := func(t *testing.T, taskID string) string {
		t.Helper()
		var logs bytes.Buffer
		h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError}))

		body := `{"taskId":"` + taskID + `","agentName":"my-agent","requestId":"req-1","status":"answered"}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for an unknown task", rec.Code)
		}
		return logs.String()
	}

	for _, taskID := range []string{"garbage", "not-a-uuid", "01a0ab9c"} {
		t.Run(taskID, func(t *testing.T) {
			if got := respond(t, taskID); got != "" {
				t.Errorf("malformed id reached the durable lookup and logged: %s", got)
			}
		})
	}

	t.Run("control: a well-formed id does reach the lookup", func(t *testing.T) {
		if got := respond(t, idgen.New()); got == "" {
			t.Error("nothing logged for a well-formed id — the lookup was never reached, so the cases above assert nothing")
		}
	})
}

func TestHandleRespond_FailedMarkerPublishStillAnswers200(t *testing.T) {
	h, mux, _ := newHILHandler(t)
	h.publishLogRecord = func(context.Context, string, []byte) error { return errors.New("broker unavailable") }
	h.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "my-agent"})
	taskID := idgen.New()
	_ = h.core.Tasks().Register(taskID, "my-agent", "sam_dev_user", entrypoint.TaskIdentity{SessionID: "sess-1"})
	h.core.Tasks().SetWaiting(taskID, "req-8")

	body := fmt.Sprintf(`{"taskId":%q,"agentName":"my-agent","requestId":"req-8","status":"answered","data":{"foo":"bar"}}`, taskID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/userInput/respond", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if id, recorded := h.core.PendingUserInput(taskID); id != "" || !recorded {
		t.Errorf("the answer must end the wait on the record: %q %v", id, recorded)
	}
}
