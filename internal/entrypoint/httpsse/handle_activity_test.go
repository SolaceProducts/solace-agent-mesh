// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func setupActivityHandlerTest(t *testing.T) *activityHandler {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	return &activityHandler{
		core:   core,
		svc:    analytics.NewService(core.Store()).WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()),
		logger: adapter.logger,
	}
}

func activityReq(target string, id *auth.Subject) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if id != nil {
		req = req.WithContext(auth.ContextWithSubject(req.Context(), id))
	}
	return req
}

func TestActivityHandler_parseQuery_FailsClosedOnMissingIdentity(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=alice", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing identity + userId=alice: status = %d, want 403; body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_NonAdminBlocksCrossUser(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=bob", id))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin cross-user: status = %d, want 403; body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_NonAdminSelfIsAllowed(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=alice", id))
	if rec.Code != http.StatusOK {
		t.Errorf("non-admin self: status = %d, want 200; body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_AdminSeesCrossUser(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "admin", Scopes: []string{rbac.ScopeAgentInvokeAny, rbac.ScopeActivityRead}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=alice", id))
	if rec.Code != http.StatusOK {
		t.Errorf("admin cross-user: status = %d, want 200; body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_PartialGlobIsNotAdmin(t *testing.T) {
	for _, granted := range []string{"activity:**:read", "a*:*:read"} {
		t.Run(granted, func(t *testing.T) {
			h := setupActivityHandlerTest(t)
			rec := httptest.NewRecorder()
			id := auth.NewTestSubject(auth.SubjectFields{UserID: "mallory", Scopes: []string{granted}})
			h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=alice", id))
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s cross-user: status = %d, want 403; body=%s", granted, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestActivityHandler_parseCommonQuery_RejectsBeyondMaxWindow(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	start := time.Now().Add(-100 * 24 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().UTC().Format(time.RFC3339)
	target := "/api/v1/activity/agents/performance?startDate=" + start + "&endDate=" + end
	h.handleAgentPerformance(rec, activityReq(target, id))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("100-day window: status = %d, want 400; body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("expected error message to mention 'exceeds', got %s", rec.Body.String())
	}
}

func TestActivityHandler_parseCommonQuery_ClampsFutureEndDate(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	start := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	end := "2100-01-01T00:00:00Z"
	target := "/api/v1/activity/agents/performance?startDate=" + start + "&endDate=" + end
	h.handleAgentPerformance(rec, activityReq(target, id))
	if rec.Code == http.StatusBadRequest {
		t.Errorf("future endDate within MaxWindow after clamp should not 400; got body=%s", rec.Body.String())
	}
}

func TestActivityHandler_parseCommonQuery_DefaultsStartDateWhenOmitted(t *testing.T) {
	h := setupActivityHandlerTest(t)
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	req := activityReq("/api/v1/activity/agents/performance", id)
	rec := httptest.NewRecorder()
	q, ok := h.parseCommonQuery(rec, req, true)
	if !ok {
		t.Fatalf("parseCommonQuery returned !ok for no-params request: %s", rec.Body.String())
	}
	if q.StartDate <= 0 {
		t.Errorf("startDate not defaulted: got %d, want > 0", q.StartDate)
	}
	windowMs := q.EndDate - q.StartDate
	wantMs := (24 * time.Hour).Milliseconds()
	if windowMs != wantMs {
		t.Errorf("defaulted window = %d ms, want %d ms (24h)", windowMs, wantMs)
	}
}

func TestActivityHandler_parseCommonQuery_RejectsStartOnlyBeyondMaxWindow(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	start := time.Now().Add(-91 * 24 * time.Hour).UTC().Format(time.RFC3339)
	target := "/api/v1/activity/agents/performance?startDate=" + start
	h.handleAgentPerformance(rec, activityReq(target, id))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("91-day window without endDate: status = %d, want 400; body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exceeds") {
		t.Errorf("expected error to mention 'exceeds'; got %s", rec.Body.String())
	}
}

func TestActivityHandler_parseCommonQuery_RejectsBadDate(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?startDate=nope", id))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad startDate: status = %d, want 400", rec.Code)
	}
}

func TestActivityHandler_handleAgentPerformance_ReturnsPaginatedEnvelope(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance", id))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data []any `json:"data"`
		Meta struct {
			Pagination map[string]any `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if body.Meta.Pagination == nil {
		t.Errorf("missing meta.pagination in response: %s", rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_EmptyScopeUnderEnforcementRejected(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	core.SetAuthzEnabled(true)
	h := &activityHandler{
		core:   core,
		svc:    analytics.NewService(core.Store()).WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()),
		logger: adapter.logger,
	}
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=bob", id))
	if rec.Code != http.StatusForbidden {
		t.Errorf("empty-scope under enforcement: status = %d, want 403; body=%s",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "empty scope") {
		t.Errorf("expected error body to mention 'empty scope'; got %q", rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_EmptyScopeWithoutEnforcementAllowed(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance", id))
	if rec.Code != http.StatusOK {
		t.Errorf("empty-scope without enforcement: status = %d, want 200; body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestActivityHandler_parseQuery_EmptyScopeCannotBecomeAdmin(t *testing.T) {
	h := setupActivityHandlerTest(t)
	rec := httptest.NewRecorder()
	id := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{}})
	h.handleAgentPerformance(rec, activityReq("/api/v1/activity/agents/performance?userId=bob", id))
	if rec.Code != http.StatusForbidden {
		t.Errorf("empty-scope cross-user (no enforcement): status = %d, want 403; body=%s",
			rec.Code, rec.Body.String())
	}
}

func seedActivityTask(t *testing.T, db *store.DB, id, userID, handlerName string) {
	t.Helper()
	start := time.Now().Add(-time.Hour).UnixMilli()
	end := start + 1000
	status := "completed"
	htype := store.TaskHandlerTypeAgent
	if err := store.NewTaskRepo(db).Create(t.Context(), &store.Task{
		ID:            id,
		UserID:        userID,
		StartTime:     start,
		EndTime:       &end,
		Status:        &status,
		ExecutionMode: "foreground",
		Kind:          store.TaskKindNormal,
		HandlerName:   &handlerName,
		HandlerType:   &htype,
	}); err != nil {
		t.Fatalf("seed task %s: %v", id, err)
	}
}

func TestActivityHandler_OwnerScopeIsolatesRows(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	h := &activityHandler{
		core:   core,
		svc:    analytics.NewService(core.Store()).WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()),
		logger: adapter.logger,
	}
	seedActivityTask(t, core.Store(), "20000000-0000-0000-0000-000000000001", "alice", "AliceOnlyAgent")
	seedActivityTask(t, core.Store(), "20000000-0000-0000-0000-000000000002", "bob", "BobOnlyAgent")

	handlerNames := func(id *auth.Subject, target string) []string {
		rec := httptest.NewRecorder()
		h.handleAgentPerformance(rec, activityReq(target, id))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data []struct {
				Agent struct {
					Name string `json:"name"`
				} `json:"agent"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
		}
		names := make([]string, 0, len(body.Data))
		for _, d := range body.Data {
			names = append(names, d.Agent.Name)
		}
		return names
	}

	alice := auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Scopes: []string{rbac.ScopeAgentInvokeAny}})
	got := handlerNames(alice, "/api/v1/activity/agents/performance")
	if !slices.Contains(got, "AliceOnlyAgent") {
		t.Errorf("alice cannot see her own task: rows=%v", got)
	}
	if slices.Contains(got, "BobOnlyAgent") {
		t.Errorf("CROSS-USER LEAK: alice's own-scope report returned bob's rows: %v", got)
	}

	admin := auth.NewTestSubject(auth.SubjectFields{UserID: "admin", Scopes: []string{rbac.ScopeAgentInvokeAny, rbac.ScopeActivityRead}})
	got = handlerNames(admin, "/api/v1/activity/agents/performance?userId=bob")
	if !slices.Contains(got, "BobOnlyAgent") {
		t.Errorf("admin drill-down into bob returned no bob rows: %v", got)
	}
	if slices.Contains(got, "AliceOnlyAgent") {
		t.Errorf("admin drill-down into bob leaked alice's rows: %v", got)
	}
}

var _ = slog.Default
