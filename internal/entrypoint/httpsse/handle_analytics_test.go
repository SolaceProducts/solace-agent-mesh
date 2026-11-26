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

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func aStr(s string) *string { return &s }
func aI64(v int64) *int64   { return &v }
func aInt(v int) *int       { return &v }

func setupAnalyticsTest(t *testing.T) (*analyticsHandler, *store.DB) {
	t.Helper()
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	ctx := context.Background()

	taskRepo := store.NewTaskRepo(db)
	const base = int64(1_700_000_000_000)
	mk := func(id, user, status string, start, end int64, in, out int) {
		task := &store.Task{ID: id, UserID: user, StartTime: start, Status: aStr(status), ExecutionMode: "foreground", Kind: store.TaskKindNormal, TotalInputTokens: aInt(in), TotalOutputTokens: aInt(out), TotalCachedInputTokens: aInt(0)}
		if end > 0 {
			task.EndTime = aI64(end)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}
	mk("10000000-0000-0000-0000-000000000001", "u1", "completed", base, base+1000, 10, 20)
	mk("10000000-0000-0000-0000-000000000002", "u1", "error", base+10, base+500, 1, 0)
	mk("10000000-0000-0000-0000-000000000003", "u2", "completed", base+20, base+8000, 5, 5)

	if err := store.NewTaskEventRepo(db).Create(ctx, &store.TaskEvent{
		ID: "20000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000001",
		CreatedTime: base, Topic: "test-ns/a2a/v1/agent/request/agent-x", Direction: "request", Payload: "{}",
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	store.SeedFeedback(t, db, &store.Feedback{
		ID: "40000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001",
		TaskID: "10000000-0000-0000-0000-000000000001", UserID: "u1", Rating: "down", Comment: aStr("bad"),
		SubjectID: "agent-x", SubjectType: store.SubjectTypeAgent,
		EntrypointType: store.EntrypointTypeWebUI,
	})

	if err := store.NewScheduledTaskRepo(db).Create(ctx, &store.ScheduledTask{
		ID: "50000000-0000-0000-0000-000000000001", Name: "nightly", Namespace: "test-ns", CreatedBy: "admin",
		ScheduleType: "cron", ScheduleExpression: "0 * * * *", Timezone: "UTC", TargetAgentName: "agent-x",
		TargetType: "agent", TaskMessage: "go", Source: "test", Enabled: true, ConsecutiveFailureCount: 2,
	}); err != nil {
		t.Fatalf("seed scheduled: %v", err)
	}

	return &analyticsHandler{core: core, svc: analytics.NewService(db), logger: adapter.logger}, db
}

func adminReq(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "admin", Scopes: []string{rbac.ScopeAnalyticsRead}})))
}

func adminContentReq(t *testing.T, target string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	return req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "admin", Scopes: []string{rbac.ScopeAnalyticsRead, rbac.ScopeAnalyticsReadContent}})))
}

func TestAnalyticsHandlers_Reports(t *testing.T) {
	h, _ := setupAnalyticsTest(t)

	cases := []struct {
		name        string
		target      string
		fn          http.HandlerFunc
		paged       bool
		wantKey     string
		contentTier bool
	}{
		{"taskSummary", "/api/v1/analytics/tasks/summary?window=24h", h.handleTaskSummary, false, "totalTasks", false},
		{"failed", "/api/v1/analytics/tasks/failed", h.handleFailedTasks, true, "", false},
		{"slow", "/api/v1/analytics/tasks/slow?thresholdMs=100", h.handleSlowTasks, true, "", false},
		{"agentPerf", "/api/v1/analytics/agents/performance", h.handleAgentPerformance, true, "", false},
		{"tokens", "/api/v1/analytics/tokens/summary?groupBy=day", h.handleTokenUsage, true, "", false},
		{"tokensUser", "/api/v1/analytics/tokens/summary?groupBy=user", h.handleTokenUsage, true, "", true},
		{"scheduled", "/api/v1/analytics/scheduledTasks/health", h.handleScheduledHealth, false, "totalEnabled", false},
		{"feedback", "/api/v1/analytics/feedback/summary", h.handleFeedbackSummary, false, "total", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			reqFn := adminReq
			if tc.contentTier {
				reqFn = adminContentReq
			}
			tc.fn(rec, reqFn(t, tc.target))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.paged {
				if _, ok := body["data"]; !ok {
					t.Errorf("paged response missing data: %s", rec.Body.String())
				}
				meta, ok := body["meta"].(map[string]any)
				if !ok || meta["pagination"] == nil {
					t.Errorf("paged response missing meta.pagination: %s", rec.Body.String())
				}
			} else if _, ok := body[tc.wantKey]; !ok {
				t.Errorf("summary missing %q: %s", tc.wantKey, rec.Body.String())
			}
		})
	}
}

func TestAnalyticsHandlers_TokenUsage_GroupByUser_NoContentTier_Returns403(t *testing.T) {
	h, _ := setupAnalyticsTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/tokens/summary?groupBy=user", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "reader", Scopes: []string{rbac.ScopeAnalyticsRead}})))
	rec := httptest.NewRecorder()
	h.handleTokenUsage(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAnalyticsHandlers_Validation(t *testing.T) {
	h, _ := setupAnalyticsTest(t)
	cases := []struct {
		name   string
		target string
		fn     http.HandlerFunc
	}{
		{"badStartDate", "/api/v1/analytics/tasks/summary?startDate=nope", h.handleTaskSummary},
		{"badEndDate", "/api/v1/analytics/tasks/summary?endDate=nope", h.handleTaskSummary},
		{"badExecutionMode", "/api/v1/analytics/tasks/summary?executionMode=sideways", h.handleTaskSummary},
		{"badPageSize", "/api/v1/analytics/tasks/failed?pageSize=9999", h.handleFailedTasks},
		{"badThreshold", "/api/v1/analytics/tasks/slow?thresholdMs=-5", h.handleSlowTasks},
		{"badGroupBy", "/api/v1/analytics/tokens/summary?groupBy=hour", h.handleTokenUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.fn(rec, adminReq(t, tc.target))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAnalyticsHandlers_AllRejectBadQuery(t *testing.T) {
	h, _ := setupAnalyticsTest(t)
	handlers := map[string]http.HandlerFunc{
		"summary":   h.handleTaskSummary,
		"failed":    h.handleFailedTasks,
		"slow":      h.handleSlowTasks,
		"agents":    h.handleAgentPerformance,
		"tokens":    h.handleTokenUsage,
		"scheduled": h.handleScheduledHealth,
		"feedback":  h.handleFeedbackSummary,
	}
	for name, fn := range handlers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			fn(rec, adminReq(t, "/x?startDate=not-a-date"))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestAnalyticsHandlers_ContentTiering(t *testing.T) {
	h, db := setupAnalyticsTest(t)
	if err := store.NewTaskRepo(db).Create(context.Background(), &store.Task{
		ID: "10000000-0000-0000-0000-0000000000ff", UserID: "alice", StartTime: 1_700_000_000_000,
		Status: aStr("error"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		InitialRequestText: aStr("my private prompt"),
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	reqWith := func(target string, scopes ...string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		return req.WithContext(auth.ContextWithSubject(req.Context(),
			auth.NewTestSubject(auth.SubjectFields{UserID: "op", Scopes: scopes})))
	}
	failedRows := func(scopes ...string) []map[string]any {
		rec := httptest.NewRecorder()
		h.handleFailedTasks(rec, reqWith("/api/v1/analytics/tasks/failed", scopes...))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Data
	}

	for _, row := range failedRows(rbac.ScopeAnalyticsRead) {
		if row["initialRequestText"] != "" {
			t.Errorf("read tier leaked prompt text: %v", row["initialRequestText"])
		}
		if row["userId"] != "" {
			t.Errorf("read tier leaked userId: %v", row["userId"])
		}
	}
	var sawPrompt, sawUser bool
	for _, row := range failedRows(rbac.ScopeAnalyticsRead, rbac.ScopeAnalyticsReadContent) {
		if row["initialRequestText"] == "my private prompt" {
			sawPrompt = true
		}
		if row["userId"] == "alice" {
			sawUser = true
		}
	}
	if !sawPrompt || !sawUser {
		t.Errorf("content tier should expose prompt + userId; sawPrompt=%v sawUser=%v", sawPrompt, sawUser)
	}

	feedback := func(scopes ...string) map[string]any {
		rec := httptest.NewRecorder()
		h.handleFeedbackSummary(rec, reqWith("/api/v1/analytics/feedback/summary", scopes...))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body
	}
	low := feedback(rbac.ScopeAnalyticsRead)
	if low["total"] == nil {
		t.Errorf("aggregate total must remain in the read tier: %v", low)
	}
	for _, c := range low["recentComments"].([]any) {
		if cm := c.(map[string]any); cm["comment"] != "" {
			t.Errorf("read tier leaked feedback comment: %v", cm["comment"])
		}
	}
	high := feedback(rbac.ScopeAnalyticsRead, rbac.ScopeAnalyticsReadContent)
	sawComment := false
	for _, c := range high["recentComments"].([]any) {
		if cm := c.(map[string]any); cm["comment"] == "bad" {
			sawComment = true
		}
	}
	if !sawComment {
		t.Errorf("content tier should expose the feedback comment text")
	}
}

func TestAnalyticsHandlers_NoIdentityShowsContent(t *testing.T) {
	h, _ := setupAnalyticsTest(t)
	rec := httptest.NewRecorder()
	h.handleFeedbackSummary(rec, httptest.NewRequest(http.MethodGet, "/api/v1/analytics/feedback/summary", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	sawComment := false
	for _, c := range body["recentComments"].([]any) {
		if cm := c.(map[string]any); cm["comment"] == "bad" {
			sawComment = true
		}
	}
	if !sawComment {
		t.Errorf("no-identity (community) request should include feedback content")
	}
}

func TestAnalyticsHandlers_ScopeGate(t *testing.T) {
	h, _ := setupAnalyticsTest(t)
	gated := scoped(rbac.ScopeAnalyticsRead, h.handleTaskSummary)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/tasks/summary", nil)
	req = req.WithContext(auth.ContextWithSubject(req.Context(),
		auth.NewTestSubject(auth.SubjectFields{UserID: "bob", Scopes: []string{"tool:other:invoke"}})))
	rec := httptest.NewRecorder()
	gated(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for missing scope", rec.Code)
	}
}

func TestAnalyticsHandler_TaskQuery(t *testing.T) {
	h, _ := setupAnalyticsTest(t)

	t.Run("grouped", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.handleTaskQuery(rec, adminReq(t, "/api/v1/analytics/tasks/query?group=status&window=30d"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data []struct {
				Group struct {
					Status *string `json:"status"`
				} `json:"group"`
				TaskCount int `json:"taskCount"`
			} `json:"data"`
			Meta struct {
				Pagination struct {
					PageNumber int `json:"pageNumber"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
		}
		if resp.Meta.Pagination.PageNumber != 1 {
			t.Errorf("missing pagination envelope: %s", rec.Body.String())
		}
		got := map[string]int{}
		for _, r := range resp.Data {
			if r.Group.Status != nil {
				got[*r.Group.Status] = r.TaskCount
			}
		}
		if got["completed"] != 2 || got["error"] != 1 {
			t.Errorf("by-status counts = %v, want completed=2 error=1", got)
		}
	})

	t.Run("granular", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.handleTaskQuery(rec, adminReq(t, "/api/v1/analytics/tasks/query?window=30d"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Data) != 3 {
			t.Errorf("granular rows = %d, want 3", len(resp.Data))
		}
	})

	t.Run("bad dim", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.handleTaskQuery(rec, adminReq(t, "/api/v1/analytics/tasks/query?group=bogus"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("bad dim status = %d, want 400", rec.Code)
		}
	})

	t.Run("user dim forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/tasks/query?group=user", nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(),
			auth.NewTestSubject(auth.SubjectFields{UserID: "admin", Scopes: []string{rbac.ScopeAnalyticsRead}})))
		rec := httptest.NewRecorder()
		h.handleTaskQuery(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("group-by-user (no content scope) status = %d, want 403", rec.Code)
		}
	})
}
