// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/jackc/pgx/v5/pgconn"
)

func asIdentity(a *apiClient, identity *auth.Subject) *apiClient {
	return &apiClient{t: a.t, core: a.core, handler: checkedAgainstSpec(a.t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.core.Handler().ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
	}))}
}

func usageReadLines(logs string) []map[string]any {
	var reads []map[string]any
	for line := range strings.Lines(logs) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "usage read" {
			reads = append(reads, entry)
		}
	}
	return reads
}

func utcDay(offset int) string {
	return time.Now().UTC().AddDate(0, 0, offset).Format(time.DateOnly)
}

func TestUsageReads_AuditEveryTenantReadAndNotTheCallersOwn(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	agent := api.seedAgent("Audited agent")
	user := api.seedUser("audited", "Audited User", "audited@example.com")
	model, workflow := idgen.New(), idgen.New()
	api.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'General', 'anthropic', 'claude', 'test', 'test', 0, 0)`, model)
	api.exec(`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'Audited workflow', '{}', 'test', 'test', 0, 0)`, workflow)
	api.file(9_876, func(e *store.SpendLedgerEntry) {
		e.UserSub, e.UserID, e.AgentID, e.ModelName, e.ModelConfigID = nil, new(user), new(agent), nil, new(model)
		e.RootKind, e.RootID = new("workflow"), new(workflow)
	})
	api.file(5_432, func(e *store.SpendLedgerEntry) {
		e.AgentName, e.RootKind, e.RootName = new("yaml-agent"), new("workflow"), new("yaml-workflow")
	})

	for _, read := range []struct{ path, route, subjectID string }{
		{"/usage/summary", "/usage/summary", ""},
		{"/usage/savings", "/usage/savings", ""},
		{"/usage/agents", "/usage/agents", ""},
		{"/usage/workflows", "/usage/workflows", ""},
		{"/usage/models", "/usage/models", ""},
		{"/usage/users", "/usage/users", ""},
		{"/usage/agents/" + agent, "/usage/agents/{id}", agent},
		{"/usage/workflows/" + workflow, "/usage/workflows/{id}", workflow},
		{"/usage/models/" + model, "/usage/models/{id}", model},
		{"/usage/users/" + user, "/usage/users/{id}", user},
		{"/usage/agents/byName?name=yaml-agent", "/usage/agents/byName", ""},
		{"/usage/workflows/byName?name=yaml-workflow", "/usage/workflows/byName", ""},
		{"/usage/models/byName?name=m", "/usage/models/byName", ""},
	} {
		before := len(usageReadLines(logs.String()))
		expectStatus(t, api.get(read.path), http.StatusOK)
		lines := usageReadLines(logs.String())[before:]
		if len(lines) != 1 {
			t.Errorf("GET %s wrote %d usage read lines, want one", read.path, len(lines))
			continue
		}
		line := lines[0]
		if line["level"] != "INFO" || line["crossUser"] != true || line["actor"] != auth.DevIdentity().UserID() ||
			line["route"] != http.MethodGet+" "+platpkg.APIV2Prefix+read.route {
			t.Errorf("GET %s audit = %v, want INFO by the caller on %s", read.path, line, read.route)
		}
		if subjectID, _ := line["subjectID"].(string); subjectID != read.subjectID {
			t.Errorf("GET %s audit subjectID = %q, want %q", read.path, subjectID, read.subjectID)
		}
		if _, isList := line["rowCount"]; isList != slices.Contains([]string{"/usage/agents", "/usage/workflows", "/usage/models", "/usage/users"}, read.route) {
			t.Errorf("GET %s audit = %v, want a row count on a list and only there", read.path, line)
		}
	}

	before := len(usageReadLines(logs.String()))
	for _, own := range []string{"/userUsage", "/userUsage/savings"} {
		expectStatus(t, api.get(own, "agent:*:read"), http.StatusOK)
	}
	if lines := usageReadLines(logs.String())[before:]; len(lines) != 0 {
		t.Errorf("the caller's own reads were audited as cross-user ones: %v", lines)
	}
	for _, figure := range []string{"98.76", "54.32", "Audited User", "audited@example.com"} {
		for _, line := range usageReadLines(logs.String()) {
			delete(line, "time")
			if raw, _ := json.Marshal(line); strings.Contains(string(raw), figure) {
				t.Errorf("audit line %s carries %q", raw, figure)
			}
		}
	}
}

func TestUsageReads_AuditNamesTheCaller(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	for name, tc := range map[string]struct{ sub, platformUserID string }{
		"before its users row": {"pre-claim-sub", ""},
		"with its users row":   {"claimed-sub", idgen.New()},
	} {
		caller := asIdentity(api.apiClient, auth.NewTestSubject(auth.SubjectFields{
			UserID: tc.sub, PlatformUserID: tc.platformUserID, Authenticated: true, Scopes: []string{"*:*:*"}}))
		before := len(usageReadLines(logs.String()))
		expectStatus(t, caller.call(http.MethodGet, "/usage/summary", nil), http.StatusOK)
		lines := usageReadLines(logs.String())[before:]
		if len(lines) != 1 || lines[0]["actor"] != tc.sub || lines[0]["actorUserID"] != tc.platformUserID {
			t.Errorf("%s: audit = %v, want actor %q and actorUserID %q", name, lines, tc.sub, tc.platformUserID)
		}
	}
}

func TestUsageReads_AnAPIKeyIsRefused(t *testing.T) {
	api := newUsageAPI(t)
	key := asIdentity(api.apiClient, holdingKey("*:*:*"))
	for _, path := range append(tenantUsageRoutePaths(), "/userUsage", "/userUsage/savings") {
		if rec := key.call(http.MethodGet, path, nil); rec.Code != http.StatusForbidden {
			t.Errorf("an API key: GET %s = %d %s, want 403", path, rec.Code, rec.Body.String())
		}
	}
}

func TestUsageReads_ADateIsTheUTCDayItFallsIn(t *testing.T) {
	api := newUsageAPI(t)
	midnight := func(offset int) string { return utcDay(offset) + "T00:00:00.000Z" }
	cases := map[string]struct {
		query, start, end string
	}{
		"bare dates":                 {"startDate=" + utcDay(-5) + "&endDate=" + utcDay(-3), midnight(-5), midnight(-2)},
		"UTC midnights":              {"startDate=" + midnight(-5) + "&endDate=" + midnight(-3), midnight(-5), midnight(-2)},
		"an offset the day before":   {"startDate=" + utcDay(-5) + "T00:00:00%2B02:00&endDate=" + utcDay(-3), midnight(-6), midnight(-2)},
		"a single day":               {"startDate=" + utcDay(-3) + "&endDate=" + utcDay(-3), midnight(-3), midnight(-2)},
		"an end later today is now":  {"startDate=" + utcDay(-1) + "&endDate=" + utcDay(0), midnight(-1), ""},
		"a window clamped to today":  {"startDate=" + utcDay(0) + "&endDate=" + utcDay(0), midnight(0), ""},
		"a date-time later that day": {"startDate=" + utcDay(-3) + "T23:59:59Z&endDate=" + utcDay(-3), midnight(-3), midnight(-2)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := api.get("/usage/summary?" + tc.query)
			expectStatus(t, rec, http.StatusOK)
			var window map[string]string
			if err := json.Unmarshal(rawUsageData(t, rec)["window"], &window); err != nil {
				t.Fatal(err)
			}
			if window["startTime"] != tc.start {
				t.Errorf("startTime = %s, want %s", window["startTime"], tc.start)
			}
			if tc.end != "" && window["endTime"] != tc.end {
				t.Errorf("endTime = %s, want %s", window["endTime"], tc.end)
			}
			if tc.end == "" && (window["endTime"] <= midnight(0) || window["endTime"] > time.Now().UTC().Format("2006-01-02T15:04:05.000Z")) {
				t.Errorf("endTime = %s, want now, not the end of today", window["endTime"])
			}
		})
	}
}

func TestUsageReads_NamesEveryBadParameterAtOnce(t *testing.T) {
	api := newUsageAPI(t)
	for path, fields := range map[string][]string{
		"/usage/agents?population=x&expand=nodes&startDate=2020-07-01&endDate=2020-07-02": {"population", "expand", "startDate"},
		"/usage/users?budgetId=nope&startDate=yesterday&endDate=" + utcDay(0):             {"budgetId", "startDate"},
		"/usage/summary?limit=5&skip=1":                                                   {"limit", "skip"},
	} {
		rec := api.get(path)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("GET %s = %d %s, want a refusal", path, rec.Code, rec.Body.String())
			continue
		}
		details := decodeError(t, rec).ValidationDetails
		for _, field := range fields {
			if len(details[field]) == 0 {
				t.Errorf("GET %s: details = %v, want %s named", path, details, field)
			}
		}
		if len(details) != len(fields) {
			t.Errorf("GET %s: details = %v, want exactly %v", path, details, fields)
		}
	}
}

func TestUsageReads_RefusesWhatItCannotAnswer(t *testing.T) {
	api := newUsageAPI(t)
	for path, want := range map[string]struct {
		status int
		field  string
	}{
		"/usage/workflows/not-a-uuid":                                                   {http.StatusBadRequest, "id"},
		"/usage/models/not-a-uuid":                                                      {http.StatusBadRequest, "id"},
		"/usage/users/not-a-uuid":                                                       {http.StatusBadRequest, "id"},
		"/usage/users/" + strings.ToUpper(idgen.New()):                                  {http.StatusBadRequest, "id"},
		"/usage/users?budgetId=" + strings.ReplaceAll(idgen.New(), "-", ""):             {http.StatusBadRequest, "budgetId"},
		"/usage/users?budgetId=" + strings.ToUpper(store.SeededMeshBudgetID):            {http.StatusBadRequest, "budgetId"},
		"/usage/agents?sort=tokens:desc":                                                {http.StatusUnprocessableEntity, "sort"},
		"/usage/agents?sort=cost:desc&sort=name":                                        {http.StatusUnprocessableEntity, "sort"},
		"/usage/users?startDate=" + utcDay(-1) + "&endDate=" + utcDay(-2):               {http.StatusUnprocessableEntity, "endDate"},
		"/usage/users?sort=budgetUsage&startDate=" + utcDay(-1) + "&endDate=9999-12-31": {http.StatusUnprocessableEntity, "endDate"},
		"/usage/summary?startDate=2020-07-01&endDate=2020-07-20":                        {http.StatusUnprocessableEntity, "startDate"},
	} {
		expectDetail(t, api.get(path), want.status, want.field)
	}
}

func TestUsageReads_EverySortAnswers(t *testing.T) {
	api := newUsageAPI(t)
	api.file(100, nil)
	sorts := []string{"cost", "shareOfSpend", "activities", "costPerActivity", "name", "uncachedInputCost", "outputCost", "cacheReadCost", "cacheWriteCost"}
	for kind, extra := range map[string][]string{"workflows": nil, "agents": nil, "users": {"budget", "budgetUsage"}, "models": {"model"}} {
		for _, sort := range append(slices.Clone(sorts), extra...) {
			for _, direction := range []string{"asc", "desc"} {
				expectStatus(t, api.get("/usage/"+kind+"?sort="+sort+":"+direction), http.StatusOK)
			}
		}
	}
}

func TestUsageReads_UsersCarryTheirBudget(t *testing.T) {
	api := newUsageAPI(t)
	user, budgetID := api.seedUser("parminder", "Parminder", "parminder@example.com"), idgen.New()
	api.exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'Procurement', 'procurement', 'user', ?, 80, 'test', 'test', 0, 0)`, budgetID, int64(450_0000000000))
	api.exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, 'test', 'test', 0, 0)`, idgen.New(), user, budgetID)
	api.file(41_230, func(e *store.SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(user) })

	rows := decodeList[UserUsageRow](t, api.get("/usage/users?sort=budgetUsage:desc")).Data
	i := slices.IndexFunc(rows, func(r UserUsageRow) bool { return r.ID != nil && *r.ID == user })
	if i < 0 {
		t.Fatalf("users = %+v, want Parminder", rows)
	}
	if row := rows[i]; row.User == nil || row.User.Email == nil || *row.User.Email != "parminder@example.com" || row.Budget == nil ||
		row.Budget.ID != budgetID || row.Budget.Status != "approaching" || row.Budget.Source != "assigned" || row.BudgetUsage == nil {
		t.Errorf("row = %+v, want Parminder approaching her assigned budget", row)
	}
	report := decodeData[UserUsageReport](t, api.get("/usage/users/"+user))
	if report.ID != user || report.Budget == nil || report.Budget.Status != "approaching" || report.Budget.Source != "assigned" {
		t.Errorf("report = %+v, want her report and budget", report)
	}
}

func TestUsageReads_ABudgetIDListsTheUsersOnThatBudget(t *testing.T) {
	api := newUsageAPI(t)
	parminder := api.seedUser("parminder", "Parminder", "parminder@example.com")
	api.seedUser("quinn", "Quinn", "quinn@example.com")
	budgetID := idgen.New()
	api.exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'Procurement', 'procurement', 'user', ?, 80, 'test', 'test', 0, 0)`, budgetID, int64(450_0000000000))
	api.exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, 'test', 'test', 0, 0)`, idgen.New(), parminder, budgetID)
	api.file(41_230, func(e *store.SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(parminder) })

	names := func(query string) []string {
		t.Helper()
		got := []string{}
		for _, row := range decodeList[UserUsageRow](t, api.get("/usage/users?period=this_month&"+query)).Data {
			if row.Name != nil {
				got = append(got, *row.Name)
			}
		}
		return got
	}
	for _, sort := range []string{"cost:desc", "name:asc", "budgetUsage:desc"} {
		if got := names("budgetId=" + budgetID + "&sort=" + sort); !slices.Equal(got, []string{"Parminder"}) {
			t.Errorf("Procurement by %s = %v, want only Parminder", sort, got)
		}
	}
	if got := names("budgetId=" + store.SeededDefaultUserBudgetID + "&search=quinn"); !slices.Equal(got, []string{"Quinn"}) {
		t.Errorf("the default's users named quinn = %v, want Quinn, who has no assignment", got)
	}
	if got := names("budgetId=" + store.SeededMeshBudgetID); len(got) != 0 {
		t.Errorf("the mesh budget's users = %v, want none", got)
	}
	if got := names("budgetId=" + missingID); len(got) != 0 {
		t.Errorf("an unknown budget's users = %v, want none", got)
	}
}

func TestUsageReads_TheSummaryGivesEachKindTheCostItsListCovers(t *testing.T) {
	api := newUsageAPI(t)
	api.file(100, func(e *store.SpendLedgerEntry) { e.AgentName = new("qa") })
	api.file(30, func(e *store.SpendLedgerEntry) { e.RootKind, e.RootName = new("workflow"), new("nightly") })
	api.file(7, nil)

	want := UsageCostByKind{Users: "1.3700000000", Agents: "1.0000000000", Workflows: "0.3000000000", Models: "1.3700000000"}
	if got := decodeData[UsageSummary](t, api.get("/usage/summary?period=this_month")).CostByKind; got != want {
		t.Errorf("costByKind = %+v, want %+v", got, want)
	}
}

func TestUsageReads_EmptyArraysAreNeverNull(t *testing.T) {
	api := newUsageAPI(t)
	user, workflow := api.seedUser("idle", "Idle", "idle@example.com"), idgen.New()
	api.exec(`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'Undefined', '{}', 'test', 'test', 0, 0)`, workflow)

	for _, kind := range []string{"workflows", "agents", "users", "models"} {
		if body := api.get("/usage/" + kind + "?population=spenders").Body.String(); !strings.Contains(body, `"data":[]`) {
			t.Errorf("%s with no spenders = %s, want data []", kind, body)
		}
	}
	if got := string(rawUsageData(t, api.get("/usage/users/"+user))["topRoots"]); got != "[]" {
		t.Errorf("topRoots = %s, want []", got)
	}
	if got := string(rawUsageData(t, api.get("/usage/workflows/"+workflow))["nodes"]); got != "[]" {
		t.Errorf("nodes = %s, want []", got)
	}
}

func TestUsageReads_ADeletedUserHasNoUser(t *testing.T) {
	api := newUsageAPI(t)
	named, unnamed := idgen.New(), idgen.New()
	api.exec(`INSERT INTO users (id, issuer, sub, display_name, provisioned_via, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'https://idp', 'system:pipeline', 'Pipeline', 'system', 'test', 'test', 0, 0)`, named)
	api.file(300, func(e *store.SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(named) })
	api.file(200, func(e *store.SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(unnamed) })
	api.exec(`DELETE FROM users WHERE id = ?`, named)

	rows := decodeList[UserUsageRow](t, api.get("/usage/users?population=spenders")).Data
	byID := map[string]UserUsageRow{}
	for _, row := range rows {
		if row.ID != nil {
			byID[*row.ID] = row
		}
	}
	if row, ok := byID[named]; !ok || row.User != nil || row.Name == nil || *row.Name != "Pipeline" {
		t.Errorf("deleted user's row = %+v, want its id and rolled-up name, no user", row)
	}
	if row, ok := byID[unnamed]; !ok || row.User != nil || row.Name != nil {
		t.Errorf("unnamed user's row = %+v, want its id, no user and no name", row)
	}
	for id, wantName := range map[string]string{named: `"Pipeline"`, unnamed: "null"} {
		raw := rawUsageData(t, api.get("/usage/users/"+id))
		if string(raw["id"]) != `"`+id+`"` || string(raw["user"]) != "null" || string(raw["name"]) != wantName {
			t.Errorf("report = id %s name %s user %s, want its id, name %s and no user", raw["id"], raw["name"], raw["user"], wantName)
		}
	}
}

func TestUsageReads_AnUnattributedUserIsListedWithoutTheirSubject(t *testing.T) {
	api := newUsageAPI(t)
	api.file(300, func(e *store.SpendLedgerEntry) { e.UserSub = new("someone@example.com") })

	rec := api.get("/usage/users?population=spenders")
	rows := decodeList[UserUsageRow](t, rec).Data
	if len(rows) != 1 || rows[0].ID != nil || rows[0].Name != nil || strings.Contains(rec.Body.String(), "someone@example.com") {
		t.Errorf("users = %s, want one row with no id, name or subject", rec.Body.String())
	}
}

func TestUsageReads_AWorkflowReportsItsFreshness(t *testing.T) {
	api := newUsageAPI(t)
	workflow := idgen.New()
	api.exec(`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'Expense Audit', '{}', 'test', 'test', 0, 0)`, workflow)
	api.file(2_000, func(e *store.SpendLedgerEntry) { e.RootKind, e.RootID = new("workflow"), new(workflow) })
	refreshed := time.Now().Add(-time.Minute).UnixMilli()
	if err := store.NewSpendRollupRepo(api.core.DB()).MarkRefreshed(t.Context(), refreshed); err != nil {
		t.Fatalf("mark refreshed: %v", err)
	}

	report := decodeData[WorkflowUsageReport](t, api.get("/usage/workflows/"+workflow))
	if int64(report.DataAsOf) != refreshed || report.HistoryComplete || report.ComparisonWindow != nil {
		t.Errorf("freshness = %v %v %v, want refreshed a minute ago, history still building, no comparison", report.DataAsOf, report.HistoryComplete, report.ComparisonWindow)
	}
}

func TestUsageReads_ExpandDailyFillsEachRowsDays(t *testing.T) {
	api := newUsageAPI(t)
	model := idgen.New()
	api.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'General', 'anthropic', 'claude', 'test', 'test', 0, 0)`, model)
	api.file(700, func(e *store.SpendLedgerEntry) {
		e.ModelName, e.ModelConfigID, e.UserSub = nil, new(model), new("bare-sub")
	})
	today := time.Now().UTC().Day()

	models := decodeList[ModelUsageRow](t, api.get("/usage/models?expand=daily&population=spenders&period=this_month")).Data
	if len(models) != 1 {
		t.Fatalf("models = %+v, want the one", models)
	}
	days := models[0].Daily
	if len(days) != today || days[today-1].Date != utcDay(0) || days[today-1].Cost != "7.0000000000" || days[today-1].Activities != 1 {
		t.Errorf("daily = %+v, want every day of the month so far, with $7 and one activity today", days)
	}
	if today > 1 && days[0].Cost != "0.0000000000" {
		t.Errorf("the first of the month = %+v, want a zero-filled day", days[0])
	}
	users := decodeList[UserUsageRow](t, api.get("/usage/users?expand=daily&population=spenders&period=this_month")).Data
	if len(users) != 1 || users[0].ID != nil || len(users[0].Daily) != today || users[0].Daily[today-1].Cost != "7.0000000000" {
		t.Errorf("users = %+v, want the bare subject's $7 today", users)
	}
}

func TestUsageReads_ModelsCarryTheProviderModelsThatServedThem(t *testing.T) {
	api := newUsageAPI(t)
	model := idgen.New()
	api.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'General', 'anthropic', 'claude', 'test', 'test', 0, 0)`, model)
	var callID string
	api.file(900, func(e *store.SpendLedgerEntry) { e.ModelName, e.ModelConfigID, callID = nil, new(model), e.ID })
	if _, err := store.NewSpendLedgerRepo(api.core.DB()).InsertDetails(t.Context(), []store.SpendLedgerDetail{
		{ID: callID, ResolvedModelName: new("claude-sonnet-4-6"), ResolvedModelProvider: new("anthropic")},
	}); err != nil {
		t.Fatalf("details: %v", err)
	}
	if err := store.NewSpendRollupRepo(api.core.DB()).RefreshDay(t.Context(), time.Now().UTC().Truncate(24*time.Hour).UnixMilli(), true); err != nil {
		t.Fatalf("roll up: %v", err)
	}

	served := func(where string, models []UsageServedModel) {
		t.Helper()
		if len(models) != 1 || models[0].Name == nil || *models[0].Name != "claude-sonnet-4-6" || models[0].Provider == nil ||
			*models[0].Provider != "anthropic" || models[0].Cost != "9.0000000000" {
			t.Errorf("%s models = %+v, want claude-sonnet-4-6's $9", where, models)
		}
	}
	rows := decodeList[ModelUsageRow](t, api.get("/usage/models?population=spenders&sort=model:asc")).Data
	if len(rows) != 1 {
		t.Fatalf("models = %+v, want the one", rows)
	}
	served("row", rows[0].Models)
	served("report", decodeData[ModelUsageReport](t, api.get("/usage/models/"+model)).Models)
}

func TestUsageReads_SavingsReconcileWithTheSummary(t *testing.T) {
	api := newUsageAPI(t)
	api.saved(1_000, 5, nil)
	api.saved(250, 0, func(e *store.SpendLedgerEntry) { e.Cost, e.AvoidedInputCost = nil, nil })
	api.file(1, nil)

	if err := store.NewSpendRollupRepo(api.core.DB()).MarkRefreshed(t.Context(), time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("mark refreshed: %v", err)
	}

	summary := rawUsageData(t, api.get("/usage/summary?period=this_month"))
	savings := rawUsageData(t, api.get("/usage/savings?period=this_month"))
	var summaryWindow, savingsWindow UsageWindow
	if json.Unmarshal(summary["window"], &summaryWindow) != nil || json.Unmarshal(savings["window"], &savingsWindow) != nil {
		t.Fatalf("windows %s and %s", summary["window"], savings["window"])
	}
	if savingsWindow.StartTime != summaryWindow.StartTime || savingsWindow.EndTime-summaryWindow.EndTime > Instant(time.Second.Milliseconds()) ||
		string(summary["dataAsOf"]) == "null" || string(summary["dataAsOf"]) != string(savings["dataAsOf"]) {
		t.Errorf("savings window %s as of %s, want the summary's %s, which ends now, as of %s", savings["window"], savings["dataAsOf"], summary["window"], summary["dataAsOf"])
	}
	var totals struct{ Calls, UnpricedCalls int64 }
	if err := json.Unmarshal(summary["totals"], &totals); err != nil {
		t.Fatal(err)
	}
	if string(savings["calls"]) != fmt.Sprint(totals.Calls) || string(savings["unpricedCalls"]) != fmt.Sprint(totals.UnpricedCalls) {
		t.Errorf("savings cover %s calls, %s unpriced; want the summary's %d and %d", savings["calls"], savings["unpricedCalls"], totals.Calls, totals.UnpricedCalls)
	}
}

func TestUsageReads_SavingsWithNoPricedMeasuredCallHaveNoCost(t *testing.T) {
	for name, extraPriced := range map[string]bool{"every call unpriced": false, "priced calls nobody measured": true} {
		t.Run(name, func(t *testing.T) {
			api := newUsageAPI(t)
			api.saved(250, 0, func(e *store.SpendLedgerEntry) { e.Cost, e.AvoidedInputCost = nil, nil })
			if extraPriced {
				api.file(3, nil)
			}
			rec := api.get("/usage/savings?period=this_month")
			got := decodeData[UsageSavings](t, rec)
			if got.AvoidedInputTokens == nil || *got.AvoidedInputTokens != 250 || got.AvoidedCost != nil || got.MeasuredCalls != 1 || got.UnpricedCalls != 1 {
				t.Errorf("savings = %+v, want 250 tokens, a null cost, one measured and unpriced call", got)
			}
			if !strings.Contains(rec.Body.String(), `"avoidedCost":null`) {
				t.Errorf("body = %s, want avoidedCost sent as null", rec.Body.String())
			}
		})
	}
}

func TestUsageReads_OwnUsage(t *testing.T) {
	api := newUsageAPI(t)
	api.file(500, nil)

	ranged := decodeData[SelfUsage](t, api.get("/userUsage?startDate="+utcDay(-2)+"&endDate="+utcDay(-1), "agent:*:read"))
	if len(ranged.Daily) != 2 || ranged.Daily[0].Date != utcDay(-2) {
		t.Errorf("own usage daily = %+v, want the two days asked for", ranged.Daily)
	}
	nobody := asIdentity(api.apiClient, auth.NewTestSubject(auth.SubjectFields{Authenticated: true}))
	rec := nobody.call(http.MethodGet, "/userUsage", nil)
	expectStatus(t, rec, http.StatusOK)
	if self := decodeData[SelfUsage](t, rec); self.UserID != nil || self.Totals.Cost != "0.0000000000" {
		t.Errorf("own usage with no subject = %+v, want zeros and no userId", self)
	}
}

func TestUsageError_AFailedConnectionLogsNoConnectionIdentity(t *testing.T) {
	refused := connectFailure(t)
	for name, tc := range map[string]struct {
		err    error
		status int
	}{
		"timed out": {fmt.Errorf("SpendUsageRepo.List: %w: %w", refused, context.DeadlineExceeded), http.StatusServiceUnavailable},
		"refused":   {fmt.Errorf("SpendUsageRepo.List: %w", refused), http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			h := Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, usageError(tc.err) }, "Unable to list agent usage")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			expectStatus(t, rec, tc.status)
			line := logs.String()
			for _, leaked := range []string{"nobody", "127.0.0.1", "none"} {
				if strings.Contains(line, leaked) {
					t.Errorf("log carries %q: %s", leaked, line)
				}
			}
			if !strings.Contains(line, "connect to the platform database") {
				t.Errorf("log = %s, want the redacted connect failure", line)
			}
		})
	}
}

func TestUsageError_AnAbandonedReadIsNotAnError(t *testing.T) {
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h := Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, usageError(fmt.Errorf("SpendUsageRepo.List: %w", context.Canceled))
	}, "Unable to list agent usage")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("an abandoned read logged at ERROR:\n%s", logs.String())
	}
}

func connectFailure(t *testing.T) error {
	t.Helper()
	conn, err := pgconn.Connect(t.Context(), "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1")
	if err == nil {
		_ = conn.Close(t.Context())
		t.Fatal("connected to a port nothing should listen on")
	}
	if _, ok := errors.AsType[*pgconn.ConnectError](err); !ok {
		t.Fatalf("connect error = %T, want *pgconn.ConnectError", err)
	}
	return err
}

func TestUsageReads_ASystemUserReadsItsOwnUsageAsOne(t *testing.T) {
	api := newUsageAPI(t)
	system := idgen.New()
	api.exec(`INSERT INTO users (id, issuer, sub, display_name, provisioned_via, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'https://sam', 'system:pipeline', 'Pipeline', 'system', 'test', 'test', 0, 0)`, system)
	api.file(800, func(e *store.SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(system) })
	api.file(9_900, nil)

	pipeline := asIdentity(api.apiClient, auth.NewTestSubject(auth.SubjectFields{UserID: "system:pipeline", PlatformUserID: system, Authenticated: true}))
	self := decodeData[SelfUsage](t, pipeline.call(http.MethodGet, "/userUsage", nil))
	if self.UserID == nil || *self.UserID != system || self.User == nil || !self.User.IsSystemUser || !self.User.IsServiceAccount || self.Totals.Cost != "8.0000000000" {
		t.Errorf("own usage = %+v, want the service account's own $8, with isSystemUser and isServiceAccount both true", self)
	}
}

func TestUsageReads_RefusalsDoNotEchoTheRequest(t *testing.T) {
	api := newUsageAPI(t)
	const sent = "zq-echo"
	for _, path := range []string{
		"/usage/summary?period=" + sent,
		"/usage/summary?startDate=" + sent + "&endDate=" + utcDay(0),
		"/usage/agents?sort=" + sent + ":desc",
		"/usage/agents?population=" + sent,
		"/usage/users?budgetId=" + sent,
		"/usage/agents/byName?name=" + strings.Repeat("z", spend.MaxFieldBytes+1),
	} {
		rec := api.get(path)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("GET %s = %d, want a refusal", path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, sent) || strings.Contains(body, strings.Repeat("z", 64)) {
			t.Errorf("GET %s answered %s, which repeats the value sent", path, body)
		}
	}
}
