// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type budgetAPI struct {
	*apiClient
}

func newBudgetAPI(t *testing.T) *budgetAPI {
	t.Helper()
	core, _ := newTestCore(t)
	return &budgetAPI{apiClient: newAPIClient(t, core)}
}

func (a *budgetAPI) createBudget(name, subjectType string, amount any) Budget {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/budgets", map[string]any{"name": name, "subjectType": subjectType, "amount": amount})
	expectStatus(a.t, rec, http.StatusCreated)
	return decodeData[Budget](a.t, rec)
}

func (a *budgetAPI) seedUser(sub, displayName, email string) string {
	a.t.Helper()
	u := &store.User{
		Issuer: "https://idp.example.com", Sub: sub, OIDCProvider: "okta", Source: "{}",
		DisplayName: NullIfZero(displayName), Email: NullIfZero(email), CreatedBy: "test", UpdatedBy: "test",
	}
	if err := store.NewUsersRepo(a.core.DB()).UpsertUserWithClaims(a.t.Context(), u, nil, store.IdentityClaimWrite{}); err != nil {
		a.t.Fatalf("seed user %s: %v", sub, err)
	}
	return u.ID
}

func (a *budgetAPI) setAmount(budgetID, amount string) {
	a.t.Helper()
	expectStatus(a.t, a.call(http.MethodPatch, "/budgets/"+budgetID, map[string]any{"amount": amount}), http.StatusOK)
}

func expectKeys(t *testing.T, what string, object map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Errorf("%s has no %q key: %v", what, key, object)
		}
	}
}

func TestBudgets_Lifecycle(t *testing.T) {
	api := newBudgetAPI(t)
	rec := api.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq engineering", "subjectType": "user", "amount": "250", "warningPercentage": 80})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[Budget](t, rec)
	expectLocation(t, rec, "/budgets/"+created.ID)
	if created.Type != "budget" || created.Name != "Zq engineering" || created.SubjectType != "user" || created.Amount == nil || *created.Amount != "250.00" ||
		created.WarningPercentage == nil || *created.WarningPercentage != 80 || created.IsDefault || created.CreatedBy == "" {
		t.Errorf("created budget = %+v", created)
	}
	raw := rawData(t, api.call(http.MethodGet, "/budgets/"+created.ID, nil))
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectKeys(t, "budget", raw, "id", "type", "name", "subjectType", "amount", "warningPercentage", "isDefault",
		"assignedCount", "hiddenAssignedCount", "pendingCount", "createdBy", "createdTime", "updatedBy", "updatedTime")

	page := decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq", nil))
	if len(page.Data) != 1 || page.Data[0].ID != created.ID {
		t.Errorf("search zq = %+v, want only %s", page.Data, created.ID)
	}

	rec = api.call(http.MethodPatch, "/budgets/"+created.ID, map[string]any{"name": "Zq platform", "amount": "300.5"})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); got.Name != "Zq platform" || *got.Amount != "300.50" || *got.WarningPercentage != 80 {
		t.Errorf("patched budget = %+v, want renamed at 300.50 keeping its threshold", got)
	}
	rec = api.call(http.MethodPatch, "/budgets/"+created.ID, map[string]any{})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); got.Name != "Zq platform" {
		t.Errorf("an empty patch changed the budget: %+v", got)
	}

	expectStatus(t, api.call(http.MethodDelete, "/budgets/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/budgets/"+created.ID, nil), http.StatusNotFound)
}

func TestBudgets_Paging(t *testing.T) {
	api := newBudgetAPI(t)
	for i := range 21 {
		api.createBudget("Zq paged "+strconv.Itoa(100+i), "user", "10")
	}
	page := decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq%20paged", nil))
	if len(page.Data) != defaultPageSize || page.Meta.Pagination.Count != 21 {
		t.Errorf("default page = %d items of %d, want %d of 21", len(page.Data), page.Meta.Pagination.Count, defaultPageSize)
	}
	page = decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq%20paged&pageSize=100&sort=name:desc", nil))
	if len(page.Data) != 21 || page.Data[0].Name != "Zq paged 120" {
		t.Errorf("one page of 100 sorted by name desc starts %q with %d items", page.Data[0].Name, len(page.Data))
	}
	if page := decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq%20paged&pageNumber=9", nil)); len(page.Data) != 0 {
		t.Errorf("a page past the end has %d items, want none", len(page.Data))
	}
	expectDetail(t, api.call(http.MethodGet, "/budgets?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgets?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgets?pageSize=ten", nil), http.StatusBadRequest, "pageSize")
	expectStatus(t, api.call(http.MethodGet, "/budgets?sort=name", nil), http.StatusOK)
	expectDetail(t, api.call(http.MethodGet, "/budgets?sort=name:up", nil), http.StatusBadRequest, "sort")
	expectDetail(t, api.call(http.MethodGet, "/budgets?sort=createdTime:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/budgets?subjectType=agentx", nil), http.StatusUnprocessableEntity, "subjectType")
	expectDetail(t, api.call(http.MethodGet, "/budgets?subjectType=", nil), http.StatusBadRequest, "subjectType")
	expectDetail(t, api.call(http.MethodGet, "/budgets?name=zq", nil), http.StatusBadRequest, "name")
}

func TestBudgets_Validation(t *testing.T) {
	api := newBudgetAPI(t)
	expectStatus(t, api.call(http.MethodPost, "/budgets", `{"name": "Zq`), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodPost, "/budgets", map[string]any{"name": 5, "subjectType": "user", "amount": "1"}), http.StatusBadRequest, "name")
	expectDetail(t, api.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq one", "subjectType": "user", "amount": "1", "isDefault": true}), http.StatusBadRequest, "isDefault")

	rec := api.call(http.MethodPost, "/budgets", map[string]any{"name": nil, "subjectType": "agentx"})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	body := decodeError(t, rec)
	for _, field := range []string{"name", "subjectType", "amount"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("create with three problems has nothing for %s: %v", field, body.ValidationDetails)
		}
	}
	rec = api.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq unlimited", "subjectType": "user", "amount": nil, "warningPercentage": 80})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if body := decodeError(t, rec); len(body.ValidationDetails["amount"]) == 0 || len(body.ValidationDetails["warningPercentage"]) == 0 {
		t.Errorf("unlimited with a threshold has details %v, want amount and warningPercentage", body.ValidationDetails)
	}
	rec = api.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq unlimited", "subjectType": "user", "amount": nil})
	expectStatus(t, rec, http.StatusCreated)
	if got := decodeData[Budget](t, rec); got.Amount != nil {
		t.Errorf("amount null created %v, want unlimited", *got.Amount)
	}
	b := api.createBudget("Zq named", "user", "1")
	expectDetail(t, api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"name": nil}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"name": "  "}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"amount": "1.234"}), http.StatusUnprocessableEntity, "amount")
	expectStatus(t, api.call(http.MethodPatch, "/budgets/"+missingID, map[string]any{"amount": "1"}), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/budgets/not-a-uuid", nil), http.StatusBadRequest, "id")
}

func TestBudgets_PatchNullClearsThreshold(t *testing.T) {
	api := newBudgetAPI(t)
	rec := api.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq warned", "subjectType": "user", "amount": "10", "warningPercentage": 50})
	expectStatus(t, rec, http.StatusCreated)
	b := decodeData[Budget](t, rec)
	rec = api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"warningPercentage": nil})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); got.WarningPercentage != nil || got.Amount == nil {
		t.Errorf("warningPercentage null = %+v, want no threshold and the amount kept", got)
	}
}

func TestBudgets_PatchNullMakesUnlimited(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq capped", "user", "10")
	rec := api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"amount": nil})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); got.Amount != nil {
		t.Errorf("amount null left %s, want unlimited", *got.Amount)
	}
	if raw := rawData(t, api.call(http.MethodGet, "/budgets/"+b.ID, nil)); raw["amount"] != nil {
		t.Errorf("unlimited budget sends amount %v, want null", raw["amount"])
	}
}

func TestBudgets_ImmutableFieldsAre422(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq fixed", "user", "10")
	rec := api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"subjectType": "user", "isDefault": true, "amount": "-1"})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	body := decodeError(t, rec)
	for _, field := range []string{"subjectType", "isDefault", "amount"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("patch has nothing for %s: %v", field, body.ValidationDetails)
		}
	}
	if got := decodeData[Budget](t, api.call(http.MethodGet, "/budgets/"+b.ID, nil)); got.IsDefault || *got.Amount != "10.00" {
		t.Errorf("a refused patch wrote: %+v", got)
	}
	expectDetail(t, api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"subjectType": nil}), http.StatusUnprocessableEntity, "subjectType")
}

func TestBudgets_MakeDefault(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq new default", "user", "10")
	rec := api.call(http.MethodPut, "/budgets/"+b.ID+"/makeDefault", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); !got.IsDefault || got.ID != b.ID {
		t.Errorf("made default = %+v", got)
	}
	if old := decodeData[Budget](t, api.call(http.MethodGet, "/budgets/"+store.SeededDefaultUserBudgetID, nil)); old.IsDefault {
		t.Errorf("the previous default is still the default")
	}
	expectStatus(t, api.call(http.MethodPut, "/budgets/"+b.ID+"/makeDefault", nil), http.StatusOK)
	expectStatus(t, api.call(http.MethodPut, "/budgets/"+missingID+"/makeDefault", nil), http.StatusNotFound)
}

func TestBudgets_ConflictsDoNotEchoNames(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq Secret Team", "user", "10")
	user := api.seedUser("zq-conflict", "Zq Hidden Person", "zq.person@example.com")
	expectStatus(t, api.call(http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": user, "budgetId": b.ID}), http.StatusCreated)

	for _, tc := range []struct {
		name     string
		rec      func() []byte
		sentence string
	}{
		{"name taken", func() []byte {
			return api.expectConflict(http.MethodPost, "/budgets", map[string]any{"name": "Zq Secret Team", "subjectType": "user", "amount": "1"})
		}, budgetConflictSentences[entplatform.BudgetConflictNameTaken]},
		{"tier default", func() []byte {
			return api.expectConflict(http.MethodDelete, "/budgets/"+store.SeededDefaultUserBudgetID, nil)
		}, budgetConflictSentences[entplatform.BudgetConflictTierDefault]},
		{"mesh", func() []byte { return api.expectConflict(http.MethodDelete, "/budgets/"+store.SeededMeshBudgetID, nil) },
			budgetConflictSentences[entplatform.BudgetConflictMeshUndeletable]},
		{"mesh exists", func() []byte {
			return api.expectConflict(http.MethodPost, "/budgets", map[string]any{"name": "Zq second mesh", "subjectType": "mesh", "amount": "1"})
		}, budgetConflictSentences[entplatform.BudgetConflictMeshExists]},
		{"subject assigned", func() []byte {
			return api.expectConflict(http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": user, "budgetId": store.SeededDefaultUserBudgetID})
		}, budgetConflictSentences[entplatform.BudgetConflictSubjectAssigned]},
	} {
		body := tc.rec()
		var e errorBody
		_ = json.Unmarshal(body, &e)
		if e.Message != tc.sentence {
			t.Errorf("%s: message %q, want %q", tc.name, e.Message, tc.sentence)
		}
		for _, secret := range []string{"Secret", "Hidden", "zq.person", "1 subject"} {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s: body echoes %q: %s", tc.name, secret, body)
			}
		}
	}
}

func (a *budgetAPI) expectConflict(method, path string, body any) []byte {
	a.t.Helper()
	rec := a.call(method, path, body)
	expectStatus(a.t, rec, http.StatusConflict)
	decodeError(a.t, rec)
	return rec.Body.Bytes()
}

func TestBudgets_Scopes(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq scoped", "user", "10")
	reader := api.as(rbac.ScopeBudgetRead)
	expectStatus(t, reader.call(http.MethodGet, "/budgets", nil), http.StatusOK)
	expectStatus(t, reader.call(http.MethodGet, "/budgets/"+b.ID, nil), http.StatusOK)
	expectStatus(t, reader.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq nope", "subjectType": "user", "amount": "1"}), http.StatusForbidden)
	expectStatus(t, reader.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"amount": "2"}), http.StatusForbidden)
	expectStatus(t, reader.call(http.MethodDelete, "/budgets/"+b.ID, nil), http.StatusForbidden)
	expectStatus(t, reader.call(http.MethodPut, "/budgets/"+b.ID+"/makeDefault", nil), http.StatusForbidden)
	expectStatus(t, api.as(rbac.ScopeBudgetUpdate).call(http.MethodPut, "/budgets/"+b.ID+"/makeDefault", nil), http.StatusForbidden)
	expectStatus(t, api.as(rbac.ScopeBudgetAssign).call(http.MethodPut, "/budgets/"+b.ID+"/makeDefault", nil), http.StatusOK)
	expectStatus(t, api.as(rbac.ScopeBudgetUpdate).call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"amount": "2"}), http.StatusOK)
	expectStatus(t, api.as(rbac.ScopeBudgetCreate).call(http.MethodPost, "/budgets", map[string]any{"name": "Zq made", "subjectType": "user", "amount": "1"}), http.StatusCreated)
	expectStatus(t, api.as(rbac.ScopeBudgetDelete).call(http.MethodDelete, "/budgets/"+missingID, nil), http.StatusForbidden)
	expectStatus(t, api.as(rbac.ScopeBudgetDelete, rbac.ScopeBudgetAssign).call(http.MethodDelete, "/budgets/"+missingID, nil), http.StatusNotFound)
	expectStatus(t, api.as(rbac.ScopeBudgetCreate).call(http.MethodGet, "/budgets", nil), http.StatusForbidden)
}

func TestBudgets_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, budgetsTag, budgetAssignmentsTag, budgetSubjectsTag)
	want := []string{
		"DELETE /budgetAssignments/{id}", "DELETE /budgets/{id}", "GET /budgetAssignments", "GET /budgetAssignments/{id}",
		"GET /budgets", "GET /budgets/{id}", "PATCH /budgetAssignments/{id}", "PATCH /budgets/{id}",
		"POST /budgetAssignments", "POST /budgets", "PUT /budgets/{id}/makeDefault",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}

func TestBudgetWire_CoversV1DTOs(t *testing.T) {
	for _, tc := range []struct {
		v1, v2  reflect.Type
		dropped map[string]string
	}{
		{reflect.TypeFor[entplatform.BudgetResponse](), reflect.TypeFor[Budget](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentResponse](), reflect.TypeFor[BudgetAssignment](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentSubject](), reflect.TypeFor[BudgetAssignmentSubject](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentUser](), reflect.TypeFor[BudgetAssignmentUser](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentIdentity](), reflect.TypeFor[BudgetAssignmentIdentity](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentBudget](), reflect.TypeFor[BudgetRef](), nil},
		{reflect.TypeFor[entplatform.UpdateBudgetAssignmentsResponse](), reflect.TypeFor[BudgetAssignmentChanges](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentMovedEntry](), reflect.TypeFor[MovedBudgetAssignment](), nil},
		{reflect.TypeFor[entplatform.BudgetAssignmentUnchangedEntry](), reflect.TypeFor[UnchangedBudgetAssignment](), nil},
		{reflect.TypeFor[entplatform.BudgetSubjectResponse](), reflect.TypeFor[BudgetSubject](), nil},
		{reflect.TypeFor[entplatform.BudgetSubjectBudget](), reflect.TypeFor[AppliedBudget](), nil},
		{reflect.TypeFor[entplatform.BudgetSubjectTopUp](), reflect.TypeFor[ActiveTopUp](), nil},
		{reflect.TypeFor[entplatform.BudgetSubjectUserIdentity](), reflect.TypeFor[BudgetSubjectUser](), nil},
		{reflect.TypeFor[entplatform.BudgetTopUpResponse](), reflect.TypeFor[BudgetTopUp](), map[string]string{
			"id": topUpHasNoID, "type": topUpHasNoID, "subject": "the top-up is addressed through its subject",
		}},
	} {
		carried := jsonKeys(tc.v2)
		for key := range jsonKeys(tc.v1) {
			if !carried[key] && tc.dropped[key] == "" {
				t.Errorf("%s.%s is neither on %s nor listed as dropped", tc.v1.Name(), key, tc.v2.Name())
			}
		}
	}
}

const topUpHasNoID = "a top-up is part of its budget subject, set with PUT /budgetSubjects/{id}/topUp"

func jsonKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for _, f := range reflect.VisibleFields(t) {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

func TestBudgets_ReadsRefuseUnreadOrRepeatedQueryParams(t *testing.T) {
	api := newBudgetAPI(t)
	for _, path := range []string{
		"/budgets?subjectType=user&subjectType=mesh",
		"/budgets?bogus=1",
		"/budgets/" + missingID + "?bogus=1",
		"/budgetAssignments?budgetId=" + missingID + "&bogus=1",
		"/budgetAssignments/" + missingID + "?bogus=1",
		"/budgetSubjects?subjectType=user&bogus=1",
		"/budgetSubjects/mesh?bogus=1",
	} {
		rec := api.call(http.MethodGet, path, nil)
		expectStatus(t, rec, http.StatusBadRequest)
		if len(decodeError(t, rec).ValidationDetails) == 0 {
			t.Errorf("GET %s names no parameter: %s", path, rec.Body.String())
		}
	}
}

func TestBudgets_ANonUUIDIDIsRefusedOnEveryRoute(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq path ids", "user", "1")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/budgets/not-a-uuid", nil},
		{http.MethodPatch, "/budgets/not-a-uuid", map[string]any{"amount": "2"}},
		{http.MethodDelete, "/budgets/not-a-uuid", nil},
		{http.MethodPut, "/budgets/not-a-uuid/makeDefault", nil},
		{http.MethodPut, "/budgets/not-a-uuid/updateAssignments", map[string]any{"subjectType": "user", "add": []string{}}},
		{http.MethodGet, "/budgets/" + strings.ToUpper(b.ID), nil},
	} {
		expectDetail(t, api.call(c.method, c.path, c.body), http.StatusBadRequest, "id")
	}
}

func TestBudgets_ListQueryErrors(t *testing.T) {
	api := newBudgetAPI(t)
	for _, c := range []struct {
		query, field string
		status       int
	}{
		{"pageNumber=x", "pageNumber", http.StatusBadRequest},
		{"subjectType=team", "subjectType", http.StatusUnprocessableEntity},
		{"sort=colour", "sort", http.StatusUnprocessableEntity},
		{"sort=:asc", "sort", http.StatusBadRequest},
	} {
		t.Run(c.query, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodGet, "/budgets?"+c.query, nil), c.status, c.field)
		})
	}
}
