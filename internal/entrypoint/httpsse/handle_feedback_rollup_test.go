// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var rollupNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func newRollupTestEnv(t *testing.T) (*http.ServeMux, *store.DB) {
	t.Helper()
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	db := adapter.core.Store()
	h := &feedbackRollupHandler{svc: analytics.NewService(db), logger: adapter.logger, now: func() time.Time { return rollupNow }}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/feedback/summaries", h.handleRollup)
	mux.HandleFunc("GET /api/v1/feedback/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	return mux, db
}

func rollupRequest(t *testing.T, query string, scopes ...string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/feedback/summaries"+query, nil)
	return req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "auditor", Scopes: scopes})))
}

func serveRollup(t *testing.T, mux *http.ServeMux, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeRollup(t *testing.T, rec *httptest.ResponseRecorder) analytics.Paginated[analytics.FeedbackRollupRow] {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var page analytics.Paginated[analytics.FeedbackRollupRow]
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return page
}

func seedRollupDown(t *testing.T, db *store.DB, subjectID string, at time.Time) {
	t.Helper()
	startMs := at.UnixMilli()
	status, name, htype := "completed", subjectID, store.TaskHandlerTypeAgent
	zero := 0
	end := startMs + 100
	task := &store.Task{
		ID: idgen.New(), UserID: "u1", StartTime: startMs, EndTime: &end,
		Status: &status, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: &name, HandlerType: &htype,
		TotalInputTokens: &zero, TotalOutputTokens: &zero, TotalCachedInputTokens: &zero,
	}
	if err := store.NewTaskRepo(db).Create(t.Context(), task); err != nil {
		t.Fatalf("create task for %s: %v", subjectID, err)
	}
	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: task.ID, UserID: "rater", SessionID: idgen.New(), Rating: "down",
		SubjectID: subjectID, SubjectType: store.SubjectTypeAgent,
		CreatedAt: startMs, UpdatedAt: startMs,
	})
}

func TestHandleFeedbackRollup_Authorization(t *testing.T) {
	tests := []struct {
		name       string
		scopes     []string
		wantStatus int
	}{
		{"agent wildcard admitted", []string{rbac.ScopeAgentReadFeedbackAny}, http.StatusOK},
		{"workflow wildcard admitted", []string{rbac.ScopeWorkflowReadFeedbackAny}, http.StatusOK},
		{"per-subject agent grant admitted", []string{rbac.AgentReadFeedbackScope("agent-1")}, http.StatusOK},
		{"trace grant alone rejected", []string{rbac.ScopeAgentReadTraceAny}, http.StatusForbidden},
		{"analytics read alone rejected", []string{rbac.ScopeAnalyticsRead}, http.StatusForbidden},
		{"no scopes rejected", nil, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux, _ := newRollupTestEnv(t)
			if rec := serveRollup(t, mux, rollupRequest(t, "", tc.scopes...)); rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestHandleFeedbackRollup_RejectsBadParams(t *testing.T) {
	tooMany := url.QueryEscape(strings.TrimSuffix(strings.Repeat("a,", 1001), ","))
	queries := map[string]string{
		"unknown groupBy":          "?groupBy=user",
		"unknown subjectType":      "?subjectType=user",
		"trend is not sortable":    "?sort=trend",
		"unknown sort direction":   "?sort=negativeRatio:up",
		"unknown granularity":      "?trendGranularity=month",
		"start after end":          "?startDate=2026-09-10T00:00:00Z&endDate=2026-09-01T00:00:00Z",
		"window over 366 days":     "?startDate=2025-01-01T00:00:00Z&endDate=2026-09-01T00:00:00Z",
		"malformed startDate":      "?startDate=yesterday",
		"pageSize over the cap":    "?pageSize=101",
		"agentIds over the cap":    "?agentIds=" + tooMany,
		"workflowIds over the cap": "?workflowIds=" + tooMany,
		"pre-1970 date bounds":     "?startDate=1900-01-01T00:00:00Z&endDate=1900-02-01T00:00:00Z",
		"tenant with day trend":    "?groupBy=tenant&trendGranularity=day",
	}

	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			mux, _ := newRollupTestEnv(t)
			rec := serveRollup(t, mux, rollupRequest(t, query, rbac.ScopeAgentReadFeedbackAny))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
			}
			if len(body.ValidationDetails) == 0 {
				t.Errorf("validationDetails empty: a 400 must name the field it rejected (body %s)", rec.Body.String())
			}
		})
	}
}

func TestHandleFeedbackRollup_DefaultWindowIsLast30Days(t *testing.T) {
	mux, db := newRollupTestEnv(t)
	seedRollupDown(t, db, "agent-1", rollupNow.AddDate(0, 0, -10))
	seedRollupDown(t, db, "agent-1", rollupNow.AddDate(0, 0, -40))

	page := decodeRollup(t, serveRollup(t, mux, rollupRequest(t, "?groupBy=tenant&trendGranularity=none", rbac.ScopeAgentReadFeedbackAny)))

	if len(page.Data) != 1 || page.Data[0].NegativeFeedback != 1 {
		t.Errorf("tenant row = %+v, want 1 negative (only the one inside 30 days)", page.Data)
	}
}

func TestHandleFeedbackRollup_StaysInsideReach(t *testing.T) {
	mux, db := newRollupTestEnv(t)
	seedRollupDown(t, db, "agent-1", rollupNow.AddDate(0, 0, -1))
	seedRollupDown(t, db, "agent-2", rollupNow.AddDate(0, 0, -1))

	page := decodeRollup(t, serveRollup(t, mux, rollupRequest(t, "", rbac.AgentReadFeedbackScope("agent-1"))))

	if len(page.Data) != 1 || page.Data[0].Subject.ID != "agent-1" {
		t.Errorf("rows = %+v, want only agent-1", page.Data)
	}
}

func TestHandleFeedbackRollup_ResponseShape(t *testing.T) {
	mux, db := newRollupTestEnv(t)
	seedRollupDown(t, db, "agent-1", rollupNow.AddDate(0, 0, -1))

	rec := serveRollup(t, mux, rollupRequest(t, "?trendGranularity=week", rbac.ScopeAgentReadFeedbackAny))
	var raw struct {
		Data []map[string]json.RawMessage `json:"data"`
		Meta map[string]json.RawMessage   `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if len(raw.Data) != 1 {
		t.Fatalf("data = %d rows, want 1", len(raw.Data))
	}
	for _, key := range []string{"subject", "totalActivities", "positiveFeedback", "negativeFeedback", "negativeRatio", "trend"} {
		if _, ok := raw.Data[0][key]; !ok {
			t.Errorf("row missing %q: %s", key, rec.Body.String())
		}
	}
	if _, ok := raw.Meta["pagination"]; !ok {
		t.Errorf("meta missing pagination: %s", rec.Body.String())
	}
}

func TestHandleFeedbackRollup_SortParamUsesFieldColonDirection(t *testing.T) {
	mux, db := newRollupTestEnv(t)
	seedRollupDown(t, db, "z-agent", rollupNow.AddDate(0, 0, -1))
	seedRollupDown(t, db, "z-agent", rollupNow.AddDate(0, 0, -1))
	seedRollupDown(t, db, "a-agent", rollupNow.AddDate(0, 0, -1))

	desc := decodeRollup(t, serveRollup(t, mux, rollupRequest(t, "?groupBy=subject&trendGranularity=none&sort=negativeFeedback:desc", rbac.ScopeAgentReadFeedbackAny)))
	if len(desc.Data) != 2 || desc.Data[0].Subject.ID != "z-agent" {
		t.Fatalf("sort=negativeFeedback:desc order = %+v, want z-agent first (2 negatives)", desc.Data)
	}

	asc := decodeRollup(t, serveRollup(t, mux, rollupRequest(t, "?groupBy=subject&trendGranularity=none&sort=negativeFeedback:asc", rbac.ScopeAgentReadFeedbackAny)))
	if len(asc.Data) != 2 || asc.Data[0].Subject.ID != "a-agent" {
		t.Fatalf("sort=negativeFeedback:asc order = %+v, want a-agent first (1 negative)", asc.Data)
	}
}

func TestHandleFeedbackRollup_LiteralRouteWinsOverID(t *testing.T) {
	mux, _ := newRollupTestEnv(t)
	if rec := serveRollup(t, mux, rollupRequest(t, "", rbac.ScopeAgentReadFeedbackAny)); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 from the rollup handler, not the {id} route", rec.Code)
	}
}

func TestRollupRejectsHourOverSevenDays(t *testing.T) {
	const dayMs = int64(24 * time.Hour / time.Millisecond)
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name    string
		spanMs  int64
		wantErr bool
	}{
		{"exactly seven days", 7 * dayMs, false},
		{"seven days and a millisecond", 7*dayMs + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, _, _, _, _ := setupTestAdapterWithDB(t)
			h := &feedbackRollupHandler{svc: analytics.NewService(adapter.core.Store()), logger: adapter.logger, now: func() time.Time { return rollupNow }}
			q := url.Values{}
			q.Set("trendGranularity", "hour")
			q.Set("startDate", start.Format(time.RFC3339))
			q.Set("endDate", time.UnixMilli(start.UnixMilli()+tc.spanMs).UTC().Format(time.RFC3339Nano))
			req := httptest.NewRequest(http.MethodGet, "/api/v1/feedback/summaries?"+q.Encode(), nil)
			_, fieldErrs := h.parseRollupQuery(req)
			if tc.wantErr && len(fieldErrs) == 0 {
				t.Fatal("want a rejection, got none")
			}
			if !tc.wantErr && len(fieldErrs) > 0 {
				t.Fatalf("want no rejection, got %v", fieldErrs)
			}
		})
	}
}

func TestRollupAcceptsHourGranularity(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)
	h := &feedbackRollupHandler{svc: analytics.NewService(adapter.core.Store()), logger: adapter.logger, now: func() time.Time { return rollupNow }}
	q := url.Values{}
	q.Set("trendGranularity", "hour")
	q.Set("startDate", rollupNow.AddDate(0, 0, -1).Format(time.RFC3339))
	q.Set("endDate", rollupNow.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/feedback/summaries?"+q.Encode(), nil)
	got, fieldErrs := h.parseRollupQuery(req)
	if len(fieldErrs) > 0 {
		t.Fatalf("hour should be a valid granularity: %v", fieldErrs)
	}
	if got.Trend != analytics.TrendHour {
		t.Fatalf("want TrendHour, got %q", got.Trend)
	}
}

func TestHandleFeedbackRollup_ReportsEveryBadParam(t *testing.T) {
	mux, _ := newRollupTestEnv(t)

	rec := serveRollup(t, mux, rollupRequest(t, "?groupBy=user&trendGranularity=month&sort=nope", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	for _, field := range []string{"groupBy", "trendGranularity", "sort"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails missing %q: got %v", field, body.ValidationDetails)
		}
	}
	for field, messages := range body.ValidationDetails {
		for _, msg := range messages {
			for _, rejected := range []string{"user", "month", "nope"} {
				if strings.Contains(msg, rejected) {
					t.Errorf("%s message echoes the rejected value %q: %q", field, rejected, msg)
				}
			}
		}
	}
}

func TestHandleFeedbackRollup_KeysEveryErrorByQueryParam(t *testing.T) {
	mux, _ := newRollupTestEnv(t)

	rec := serveRollup(t, mux, rollupRequest(t, "?pageNumber=0&pageSize=500&startDate=nope&endDate=also-nope", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	for _, field := range []string{"pageNumber", "pageSize", "startDate", "endDate"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails missing %q: got %v", field, body.ValidationDetails)
		}
	}
	for _, grouped := range []string{"pagination", "dateRange"} {
		if _, ok := body.ValidationDetails[grouped]; ok {
			t.Errorf("validationDetails keyed by %q, which is not a query parameter: %v", grouped, body.ValidationDetails)
		}
	}
}

func TestHandleFeedbackRollup_ReportsAPre1970BoundBesideAnUnparseableOne(t *testing.T) {
	mux, _ := newRollupTestEnv(t)

	rec := serveRollup(t, mux, rollupRequest(t, "?startDate=nope&endDate=1960-01-01T00:00:00Z", rbac.ScopeAgentReadFeedbackAny))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	for _, field := range []string{"startDate", "endDate"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails missing %q: got %v", field, body.ValidationDetails)
		}
	}
}

func TestHandleFeedbackRollup_TenantDefaultsToNoTrend(t *testing.T) {
	mux, db := newRollupTestEnv(t)
	seedRollupDown(t, db, "agent-1", rollupNow.AddDate(0, 0, -1))

	page := decodeRollup(t, serveRollup(t, mux, rollupRequest(t, "?groupBy=tenant", rbac.ScopeAgentReadFeedbackAny)))

	if len(page.Data) != 1 || page.Data[0].Subject != nil {
		t.Fatalf("rows = %+v, want one tenant row with no subject", page.Data)
	}
	if len(page.Data[0].Trend) != 0 {
		t.Errorf("trend = %+v, want none", page.Data[0].Trend)
	}
}
