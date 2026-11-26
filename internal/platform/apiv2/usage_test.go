// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

var (
	usageMoneyRE   = regexp.MustCompile(`^-?\d+\.\d{10}$`)
	usageInstantRE = regexp.MustCompile(`^"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z"$`)
)

type usageAPI struct {
	*apiClient
}

func newUsageAPI(t *testing.T) *usageAPI {
	t.Helper()
	core, _ := newTestCore(t)
	return &usageAPI{apiClient: newAPIClient(t, core)}
}

func (a *usageAPI) exec(query string, args ...any) {
	a.t.Helper()
	if _, err := a.core.DB().ExecContext(a.t.Context(), query, args...); err != nil {
		a.t.Fatalf("exec %q: %v", query, err)
	}
}

func (a *usageAPI) file(cents int64, edit func(*store.SpendLedgerEntry)) {
	a.t.Helper()
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour)
	entry := store.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: day.Add(now.Sub(day) / 2).UnixMilli(), UserSub: new("someone"), Surface: "agent",
		ModelName: new("m"), TraceID: new(idgen.New()), UncachedInputTokens: 1,
		Cost: &spend.Cost{UncachedInput: cents * 100_000_000},
	}
	if edit != nil {
		edit(&entry)
	}
	if _, err := store.NewSpendLedgerRepo(a.core.DB()).Insert(a.t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		a.t.Fatalf("file a call: %v", err)
	}
	if err := store.NewSpendRollupRepo(a.core.DB()).RefreshDay(a.t.Context(), day.UnixMilli(), true); err != nil {
		a.t.Fatalf("roll up: %v", err)
	}
}

func (a *usageAPI) seedUser(sub, displayName, email string) string {
	a.t.Helper()
	u := &store.User{Issuer: "https://idp.example.com", Sub: sub, Source: "{}", DisplayName: new(displayName), Email: new(email),
		CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewUsersRepo(a.core.DB()).UpsertUser(a.t.Context(), u, nil); err != nil {
		a.t.Fatalf("seed user %s: %v", sub, err)
	}
	return u.ID
}

func (a *usageAPI) get(path string, scopes ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.call(http.MethodGet, path, nil, scopes...)
}

func rawUsageData(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	return decodeData[map[string]json.RawMessage](t, rec)
}

func expectMoney(t *testing.T, where, value string) {
	t.Helper()
	if !usageMoneyRE.MatchString(value) {
		t.Errorf("%s = %q, want a decimal with ten fractional digits", where, value)
	}
}

func tenantUsageRoutePaths() []string {
	return []string{
		"/usage/summary", "/usage/workflows", "/usage/agents", "/usage/users", "/usage/models",
		"/usage/workflows/" + idgen.New(), "/usage/agents/" + idgen.New(), "/usage/models/" + idgen.New(), "/usage/users/" + idgen.New(),
		"/usage/workflows/byName?name=w", "/usage/agents/byName?name=a", "/usage/models/byName?name=m", "/usage/savings",
	}
}

func TestUsage_GateIsStrictAndLogsRefusal(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	for _, path := range tenantUsageRoutePaths() {
		for _, scopes := range [][]string{{"a*:*:read_usage"}, {"**:*:read_usage"}, {"agent:*:read_usage"}, {"agent:*:read", rbac.ScopeBudgetRead}} {
			rec := api.get(path, scopes...)
			expectStatus(t, rec, http.StatusForbidden)
			if !strings.Contains(rec.Body.String(), rbac.ScopeReadUsage) {
				t.Errorf("GET %s as %v = %s, want v1's refusal naming the scope", path, scopes, rec.Body.String())
			}
		}
		if rec := api.anonymous().call(http.MethodGet, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with no identity = %d, want 401", path, rec.Code)
		}
	}
	for _, scope := range []string{rbac.ScopeReadUsage, "*:*:*"} {
		expectStatus(t, api.get("/usage/summary", scope), http.StatusOK)
	}
	refusals := 0
	for line := range strings.Lines(logs.String()) {
		if !strings.Contains(line, "spend read refused") {
			continue
		}
		refusals++
		if !strings.Contains(line, `"level":"WARN"`) || strings.Contains(line, "read_usage") || strings.Contains(line, "agent:*:read") {
			t.Errorf("refusal logged as %s, want WARN naming none of the caller's scopes", line)
		}
	}
	if refusals == 0 {
		t.Errorf("no refusal logged:\n%s", logs.String())
	}
}

func TestUsageSummary_Body(t *testing.T) {
	api := newUsageAPI(t)
	api.file(1_250, nil)
	rec := api.get("/usage/summary")
	expectStatus(t, rec, http.StatusOK)
	raw := rawUsageData(t, rec)
	for _, key := range []string{"id", "type"} {
		if _, ok := raw[key]; ok {
			t.Errorf("summary carries %q; it is an answer, not a resource: %s", key, rec.Body.String())
		}
	}
	for _, key := range []string{"comparisonWindow", "comparison", "meshBudget", "dataAsOf"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("summary leaves out %q; a field with no value is null: %s", key, rec.Body.String())
		}
	}
	sum := decodeData[UsageSummary](t, rec)
	if sum.Scope != "tenant" || sum.Currency != "USD" || sum.Totals.Cost != "12.5000000000" || sum.Totals.Calls != 1 {
		t.Errorf("summary = %+v, want the tenant's one $12.50 call in USD", sum)
	}
	expectMoney(t, "costByKind.users", sum.CostByKind.Users)
	expectMoney(t, "costByBucket.output", sum.Totals.CostByBucket.Output)
	if len(sum.Daily) == 0 || !slices.ContainsFunc(sum.Daily, func(d UsageDay) bool { return d.Cost == "12.5000000000" }) {
		t.Errorf("daily = %+v, want today's $12.50", sum.Daily)
	}
	var window map[string]json.RawMessage
	if err := json.Unmarshal(raw["window"], &window); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"startTime", "endTime"} {
		if !usageInstantRE.Match(window[key]) {
			t.Errorf("window.%s = %s, want an ISO 8601 instant in ms with Z", key, window[key])
		}
	}
}

func TestUserUsage_SignedInNoScope(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	api.file(800, func(e *store.SpendLedgerEntry) { e.UserSub = new(auth.DevIdentity().UserID()) })
	api.file(9_900, nil)
	rec := api.get("/userUsage", "agent:*:read")
	expectStatus(t, rec, http.StatusOK)
	raw := rawUsageData(t, rec)
	for _, key := range []string{"id", "type"} {
		if _, ok := raw[key]; ok {
			t.Errorf("self usage carries %q: %s", key, rec.Body.String())
		}
	}
	if _, ok := raw["userId"]; !ok {
		t.Errorf("self usage has no userId key: %s", rec.Body.String())
	}
	self := decodeData[SelfUsage](t, rec)
	if self.Scope != "self" || self.Totals.Cost != "8.0000000000" || self.Totals.ShareOfSpend != nil || len(self.Months) != 3 ||
		self.Months[2].Month != time.Now().UTC().Format("2006-01") || self.Months[2].Cost != "8.0000000000" {
		t.Errorf("self = %+v, want only the caller's own $8, no share and three months ending this one", self)
	}
	if rec := api.anonymous().call(http.MethodGet, "/userUsage", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no identity = %d, want 401", rec.Code)
	}
	if strings.Contains(logs.String(), `"usage read"`) {
		t.Errorf("the caller's own read was audited as a cross-user one:\n%s", logs.String())
	}
}

func TestUserUsage_CallerWithoutAUserRecordReadsZero(t *testing.T) {
	api := newUsageAPI(t)
	api.file(9_900, nil)
	rec := api.get("/userUsage")
	expectStatus(t, rec, http.StatusOK)
	if raw := rawUsageData(t, rec); string(raw["userId"]) != "null" {
		t.Errorf("userId = %s, want null for a caller with no user record", raw["userId"])
	}
	self := decodeData[SelfUsage](t, rec)
	if self.Totals.Cost != "0.0000000000" || len(self.Months) != 3 || self.Months[2].Cost != "0.0000000000" {
		t.Errorf("self = %+v, want a zero report over three months", self)
	}
}

func (a *usageAPI) saved(tokens, cents int64, edit func(*store.SpendLedgerEntry)) {
	a.t.Helper()
	a.file(1, func(e *store.SpendLedgerEntry) {
		e.AvoidedInputTokens = new(tokens)
		e.AvoidedInputCost = new(cents * 100_000_000)
		if edit != nil {
			edit(e)
		}
	})
}

func TestUsageSavings_TenantAndOwn(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	me := func(e *store.SpendLedgerEntry) { e.UserSub = new(auth.DevIdentity().UserID()) }
	api.saved(1_000, 5, me)
	api.saved(500, 2, nil)
	api.saved(250, 0, func(e *store.SpendLedgerEntry) { e.Cost, e.AvoidedInputCost = nil, nil })
	api.file(1, nil)

	rec := api.get("/usage/savings")
	expectStatus(t, rec, http.StatusOK)
	tenant := decodeData[UsageSavings](t, rec)
	if tenant.Scope != "tenant" || tenant.Currency != "USD" || tenant.AvoidedInputTokens == nil || *tenant.AvoidedInputTokens != 1_750 ||
		tenant.Calls != 4 || tenant.MeasuredCalls != 3 || tenant.UnpricedCalls != 1 {
		t.Errorf("tenant savings = %+v, want 1750 tokens over 4 calls, 3 measured, 1 unpriced", tenant)
	}
	if tenant.AvoidedCost == nil || *tenant.AvoidedCost != "0.0700000000" {
		t.Errorf("tenant avoidedCost = %v, want 7 cents from the two priced calls", tenant.AvoidedCost)
	}

	rec = api.get("/userUsage/savings", "agent:*:read")
	expectStatus(t, rec, http.StatusOK)
	own := decodeData[UsageSavings](t, rec)
	if own.Scope != "self" || own.AvoidedInputTokens == nil || *own.AvoidedInputTokens != 1_000 || own.Calls != 1 ||
		own.AvoidedCost == nil || *own.AvoidedCost != "0.0500000000" {
		t.Errorf("own savings = %+v, want only the caller's 1000 tokens and 5 cents", own)
	}
	if rec := api.anonymous().call(http.MethodGet, "/userUsage/savings", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no identity = %d, want 401", rec.Code)
	}
	if n := strings.Count(logs.String(), `"usage read"`); n != 1 {
		t.Errorf("usage read lines = %d, want one for the tenant read and none for the caller's own", n)
	}
}

func TestUsageSavings_UnmeasuredWindowIsNullNotZero(t *testing.T) {
	api := newUsageAPI(t)
	api.file(1, nil)
	api.file(2, nil)
	data := rawUsageData(t, api.get("/usage/savings"))
	if string(data["calls"]) != "2" || string(data["measuredCalls"]) != "0" {
		t.Errorf("savings over two priced, unmeasured calls = %s calls, %s measured; want 2 and 0", data["calls"], data["measuredCalls"])
	}
	for _, key := range []string{"avoidedInputTokens", "avoidedCost", "dataAsOf"} {
		if _, ok := data[key]; !ok {
			t.Errorf("savings has no %q key", key)
		}
	}
	if string(data["avoidedInputTokens"]) != "null" || string(data["avoidedCost"]) != "null" {
		t.Errorf("savings with nothing measured = %s / %s, want null / null", data["avoidedInputTokens"], data["avoidedCost"])
	}
}

func TestUsage_CrossUserReadAuditCarriesNoNameOrFigure(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	user := api.seedUser("audit-sub", "Audrey Auditee", "audrey@example.com")
	api.file(4_321, func(e *store.SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(user) })

	expectStatus(t, api.get("/usage/users/"+user), http.StatusOK)
	expectStatus(t, api.get("/usage/users"), http.StatusOK)

	var reads []map[string]any
	for line := range strings.Lines(logs.String()) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "usage read" {
			reads = append(reads, entry)
		}
	}
	if len(reads) != 2 {
		t.Fatalf("usage read lines = %v, want one per read", reads)
	}
	if reads[0]["route"] != "GET /api/v2/platform/usage/users/{id}" || reads[0]["subjectID"] != user || reads[0]["crossUser"] != true {
		t.Errorf("detail audit = %v, want the v2 route and the user's id", reads[0])
	}
	if reads[1]["route"] != "GET /api/v2/platform/usage/users" || reads[1]["rowCount"] == nil {
		t.Errorf("list audit = %v, want the v2 route and its row count", reads[1])
	}
	for _, read := range reads {
		delete(read, "time")
		line, _ := json.Marshal(read)
		for _, secret := range []string{"Audrey Auditee", "audrey@example.com", "43.21"} {
			if strings.Contains(string(line), secret) {
				t.Errorf("audit line %s carries %q", line, secret)
			}
		}
	}
}

func TestUsage_AuditsTenantReadsUnderTheirRoute(t *testing.T) {
	logs := captureLogs(t)
	api := newUsageAPI(t)
	api.file(5, nil)
	paths := []string{"/usage/summary", "/usage/savings", "/usage/models/byName?name=m", "/usage/agents"}
	for _, path := range paths {
		expectStatus(t, api.get(path), http.StatusOK)
	}
	expectStatus(t, api.get("/userUsage"), http.StatusOK)
	expectStatus(t, api.get("/userUsage/savings"), http.StatusOK)

	var routes []string
	for line := range strings.Lines(logs.String()) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "usage read" {
			routes = append(routes, fmt.Sprint(entry["route"]))
		}
	}
	want := []string{
		"GET /api/v2/platform/usage/summary", "GET /api/v2/platform/usage/savings",
		"GET /api/v2/platform/usage/models/byName", "GET /api/v2/platform/usage/agents",
	}
	if !slices.Equal(routes, want) {
		t.Errorf("audited routes = %v, want %v and no line for the caller's own reads", routes, want)
	}
}

func TestUsage_WindowValidation(t *testing.T) {
	api := newUsageAPI(t)
	today := time.Now().UTC().Format(time.DateOnly)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	for name, tc := range map[string]struct {
		query  string
		status int
		fields []string
	}{
		"bad date":                   {"startDate=yesterday&endDate=" + today, http.StatusBadRequest, []string{"startDate"}},
		"bad date and a period too":  {"startDate=nope&endDate=" + today + "&period=this_month", http.StatusBadRequest, []string{"startDate", "period"}},
		"empty period":               {"period=", http.StatusBadRequest, []string{"period"}},
		"unknown period":             {"period=yesterday", http.StatusUnprocessableEntity, []string{"period"}},
		"period and dates":           {"period=this_month&startDate=" + today + "&endDate=" + today, http.StatusUnprocessableEntity, []string{"period"}},
		"a lone start":               {"startDate=" + today, http.StatusUnprocessableEntity, []string{"endDate"}},
		"a start in the future":      {"startDate=" + tomorrow + "&endDate=" + tomorrow, http.StatusUnprocessableEntity, []string{"startDate"}},
		"a date-time is its UTC day": {"startDate=" + today + "T23:59:59Z&endDate=" + today, http.StatusOK, nil},
		"a named window":             {"period=last_7_days", http.StatusOK, nil},
	} {
		for _, path := range []string{"/usage/summary", "/usage/agents", "/userUsage"} {
			rec := api.get(path + "?" + tc.query)
			if rec.Code != tc.status {
				t.Errorf("%s: GET %s?%s = %d %s, want %d", name, path, tc.query, rec.Code, rec.Body.String(), tc.status)
				continue
			}
			if tc.fields == nil {
				continue
			}
			body := decodeError(t, rec)
			for _, field := range tc.fields {
				if len(body.ValidationDetails[field]) == 0 {
					t.Errorf("%s: GET %s?%s names no problem with %s: %s", name, path, tc.query, field, rec.Body.String())
				}
			}
		}
	}
}

func TestUsage_UnknownParamIs400(t *testing.T) {
	api := newUsageAPI(t)
	for _, path := range []string{
		"/usage/summary?pageSize=5", "/usage/agents?name=x", "/usage/agents?budgetId=" + idgen.New(),
		"/usage/agents/" + idgen.New() + "?search=x", "/usage/agents/byName?name=a&search=x", "/userUsage?scope=tenant",
	} {
		expectStatus(t, api.get(path), http.StatusBadRequest)
	}
}

func TestUsageError_BusyIs503WithRetryAfter(t *testing.T) {
	logs := captureLogs(t)
	h := Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, usageError(fmt.Errorf("read rollups: %w", context.DeadlineExceeded))
	}, "Unable to get the usage summary")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	expectStatus(t, rec, http.StatusServiceUnavailable)
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
	if body := decodeError(t, rec); body.Message != msgUsageBusy {
		t.Errorf("message = %q, want %q", body.Message, msgUsageBusy)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("a busy database logged at ERROR, which logging.md keeps for what a human must act on:\n%s", logs.String())
	}
}

func TestUsage_AbandonedReadIsNotAnswered(t *testing.T) {
	api := newUsageAPI(t)
	api.file(5, nil)
	logs := captureLogs(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, path := range []string{"/usage/summary", "/usage/agents", "/userUsage"} {
		req := newJSONRequest(t, http.MethodGet, path, nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		auth.DevMiddleware()(api.core.Handler()).ServeHTTP(rec, req)
		if rec.Body.Len() != 0 {
			t.Errorf("GET %s abandoned by the client was answered %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	if strings.Contains(logs.String(), `"http error response"`) {
		t.Errorf("an abandoned read was logged as an error response:\n%s", logs.String())
	}
}

func TestUsageError_MapsTheServiceErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
	}{
		"not found": {fmt.Errorf("subject: %w", usage.ErrNotFound), http.StatusNotFound},
		"problems":  {usage.ValidationErrors{{Field: "period", Message: "period must be this_month"}}, http.StatusUnprocessableEntity},
		"anything":  {fmt.Errorf("boom"), http.StatusInternalServerError},
	} {
		h := Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, usageError(tc.err) }, "Unable to get agent usage")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d; body %s", name, rec.Code, tc.status, rec.Body.String())
		}
	}
}

func TestUsageWire_CoversV1DTOs(t *testing.T) {
	changed := map[string]string{
		"UsageUserRowResponse.bookedName": "dropped: always null on a users row",
		"UsageSelfResponse.id":            "renamed userId: it names the caller's user, not the report",
		"UsageWorkflowNode.nodeId":        "renamed key: the node's id in the definition, which no route fetches",
		"UsageTopUp.id":                   "dropped: a top-up is set through its subject and has no id on /api/v2",
	}
	for v1, v2 := range map[reflect.Type]reflect.Type{
		reflect.TypeFor[entplatform.UsageSummaryResponse]():  reflect.TypeFor[UsageSummary](),
		reflect.TypeFor[entplatform.UsageRowResponse]():      reflect.TypeFor[UsageRow](),
		reflect.TypeFor[entplatform.UsageModelRowResponse](): reflect.TypeFor[ModelUsageRow](),
		reflect.TypeFor[entplatform.UsageUserRowResponse]():  reflect.TypeFor[UserUsageRow](),
		reflect.TypeFor[entplatform.UsageSubjectResponse]():  reflect.TypeFor[UsageReport](),
		reflect.TypeFor[entplatform.UsageModelResponse]():    reflect.TypeFor[ModelUsageReport](),
		reflect.TypeFor[entplatform.UsageWorkflowResponse](): reflect.TypeFor[WorkflowUsageReport](),
		reflect.TypeFor[entplatform.UsageUserResponse]():     reflect.TypeFor[UserUsageReport](),
		reflect.TypeFor[entplatform.UsageSelfResponse]():     reflect.TypeFor[SelfUsage](),
		reflect.TypeFor[entplatform.UsageWindow]():           reflect.TypeFor[UsageWindow](),
		reflect.TypeFor[entplatform.UsageCostByBucket]():     reflect.TypeFor[UsageCostByBucket](),
		reflect.TypeFor[entplatform.UsageTokens]():           reflect.TypeFor[UsageTokens](),
		reflect.TypeFor[entplatform.UsageTotals]():           reflect.TypeFor[UsageTotals](),
		reflect.TypeFor[entplatform.UsageSubjectTotals]():    reflect.TypeFor[UsageSubjectTotals](),
		reflect.TypeFor[entplatform.UsageDay]():              reflect.TypeFor[UsageDay](),
		reflect.TypeFor[entplatform.UsageComparison]():       reflect.TypeFor[UsageComparison](),
		reflect.TypeFor[entplatform.UsageTopUp]():            reflect.TypeFor[UsageTopUp](),
		reflect.TypeFor[entplatform.UsageBudget]():           reflect.TypeFor[UsageBudget](),
		reflect.TypeFor[entplatform.UsageBudgetWindow]():     reflect.TypeFor[UsageBudgetWindow](),
		reflect.TypeFor[entplatform.UsageUser]():             reflect.TypeFor[UsageUser](),
		reflect.TypeFor[entplatform.UsageServedModel]():      reflect.TypeFor[UsageServedModel](),
		reflect.TypeFor[entplatform.UsageCostByKind]():       reflect.TypeFor[UsageCostByKind](),
		reflect.TypeFor[entplatform.UsageWorkflowNode]():     reflect.TypeFor[UsageWorkflowNode](),
		reflect.TypeFor[entplatform.UsageRoot]():             reflect.TypeFor[UsageRoot](),
		reflect.TypeFor[entplatform.UsageMonth]():            reflect.TypeFor[UsageMonth](),
	} {
		have := wireKeys(v2)
		for _, key := range wireKeys(v1) {
			if !slices.Contains(have, key) && changed[v1.Name()+"."+key] == "" {
				t.Errorf("%s.%s has no field in %s and no recorded reason", v1.Name(), key, v2.Name())
			}
		}
	}
}

func wireKeys(t reflect.Type) []string {
	var keys []string
	for _, f := range reflect.VisibleFields(t) {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.IsExported() && !f.Anonymous && name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

func TestUsage_RefusesUnreadOrRepeatedQueryParams(t *testing.T) {
	api := newUsageAPI(t)
	for _, path := range append(tenantUsageRoutePaths(), "/userUsage", "/userUsage/savings") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		for param, query := range map[string]string{"bogus": "bogus=1", "period": "period=this_month&period=last_month"} {
			rec := api.get(path+sep+query, "*:*:*")
			expectStatus(t, rec, http.StatusBadRequest)
			if len(decodeError(t, rec).ValidationDetails[param]) == 0 {
				t.Errorf("GET %s%s%s names no %s: %s", path, sep, query, param, rec.Body.String())
			}
		}
	}
}
