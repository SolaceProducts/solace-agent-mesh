// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestBudgets_AmountMustBeAString(t *testing.T) {
	api := newBudgetAPI(t)
	expectDetail(t, api.call(http.MethodPost, "/budgets", `{"name":"Zq number","subjectType":"user","amount":19.99}`), http.StatusBadRequest, "amount")
	b := api.createBudget("Zq string", "user", "10")
	expectDetail(t, api.call(http.MethodPatch, "/budgets/"+b.ID, `{"amount":5}`), http.StatusBadRequest, "amount")
}

func TestBudgets_UndeclaredBodyKeysAre400(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq undeclared keys", "user", "100")
	one := "/budgets/" + b.ID
	for _, c := range []struct{ name, method, path, body, field string }{
		{"miscased isDefault on create", http.MethodPost, "/budgets", `{"name":"Zq miscased","subjectType":"user","amount":"1","IsDefault":true}`, "IsDefault"},
		{"null isDefault on create", http.MethodPost, "/budgets", `{"name":"Zq null default","subjectType":"user","amount":"1","isDefault":null}`, "isDefault"},
		{"undeclared key on patch", http.MethodPatch, one, `{"colour":"red"}`, "colour"},
	} {
		t.Run(c.name, func(t *testing.T) {
			expectDetail(t, api.call(c.method, c.path, c.body), http.StatusBadRequest, c.field)
		})
	}
	if got := decodeData[Budget](t, api.call(http.MethodGet, one, nil)); got.Amount == nil || *got.Amount != "100.00" {
		t.Errorf("after the refused PATCHes, amount = %v, want 100.00 unchanged", got.Amount)
	}
}

func TestBudgets_PatchReportsEveryProblemAtOnce(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq every problem", "user", "10")
	rec := api.call(http.MethodPatch, "/budgets/"+b.ID, `{"name":null,"warningPercentage":500,"amount":"1.234"}`)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"name", "warningPercentage", "amount"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s: %v", field, details)
		}
	}
}

func TestBudgets_PatchClearingAmountUnderAThresholdIs422(t *testing.T) {
	api := newBudgetAPI(t)
	rec := api.call(http.MethodPost, "/budgets", map[string]any{"name": "Zq threshold", "subjectType": "user", "amount": "500", "warningPercentage": 80})
	expectStatus(t, rec, http.StatusCreated)
	b := decodeData[Budget](t, rec)
	rec = api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{"amount": nil})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if details := decodeError(t, rec).ValidationDetails; len(details["amount"]) == 0 || len(details["warningPercentage"]) == 0 {
		t.Errorf("validationDetails = %v, want amount and warningPercentage", details)
	}
}

func TestBudgets_EmptyPatchDoesNotTouchTheRow(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq untouched", "user", "10")
	rec := api.call(http.MethodPatch, "/budgets/"+b.ID, map[string]any{})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); got.UpdatedTime != b.UpdatedTime || got.UpdatedBy != b.UpdatedBy {
		t.Errorf("an empty PATCH bumped the row: updatedTime %v→%v", b.UpdatedTime, got.UpdatedTime)
	}
	expectStatus(t, api.call(http.MethodPatch, "/budgets/"+b.ID, ""), http.StatusBadRequest)
}

func TestBudgets_MakeDefaultIsAPutVerb(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq verb", "user", "1")
	expectStatus(t, api.call(http.MethodDelete, "/budgets/"+b.ID+"/makeDefault", nil), http.StatusMethodNotAllowed)
	expectStatus(t, api.call(http.MethodPut, "/budgets/"+b.ID+"/default", nil), http.StatusNotFound)

	rec := api.call(http.MethodPut, "/budgets/"+store.SeededMeshBudgetID+"/makeDefault", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); !got.IsDefault || got.SubjectType != "mesh" {
		t.Errorf("making the mesh budget the default = %+v, want it, already its tier's default", got)
	}
}

func TestBudgets_ListPagesConcatenate(t *testing.T) {
	api := newBudgetAPI(t)
	for _, name := range []string{"delta", "alpha", "charlie", "bravo", "echo"} {
		api.createBudget("Zq pages "+name, "user", "1")
	}
	api.createBudget("Zq pages Éngineering", "user", "1")

	var raw struct {
		Meta struct {
			Pagination map[string]any `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(api.callRaw(http.MethodGet, "/budgets?search=zq%20pages&pageSize=2", nil), &raw); err != nil {
		t.Fatalf("decode the first page: %v", err)
	}
	expectKeys(t, "meta.pagination", raw.Meta.Pagination, "pageNumber", "count", "pageSize", "nextPage", "totalPages")

	var paged []string
	for n := 1; n <= 3; n++ {
		for _, b := range decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq%20pages&pageSize=2&pageNumber="+strconv.Itoa(n), nil)).Data {
			paged = append(paged, b.Name)
		}
	}
	var all []string
	for _, b := range decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq%20pages&pageSize=100", nil)).Data {
		all = append(all, b.Name)
	}
	if len(all) != 6 || !slices.Equal(paged, all) {
		t.Errorf("pages %v do not concatenate to the full list %v", paged, all)
	}

	if got := decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=%C3%89", nil)).Data; len(got) != 1 || got[0].Name != "Zq pages Éngineering" {
		t.Errorf("search É = %+v, want only Zq pages Éngineering", got)
	}
}

func TestBudgets_ListSorts(t *testing.T) {
	api := newBudgetAPI(t)
	api.createBudget("Zq sort small", "user", "5")
	api.createBudget("Zq sort open", "user", nil)
	api.createBudget("Zq sort big", "user", "50")
	for _, c := range []struct {
		sort string
		want []string
	}{
		{"", []string{"Zq sort big", "Zq sort open", "Zq sort small"}},
		{"&sort=name:desc", []string{"Zq sort small", "Zq sort open", "Zq sort big"}},
		{"&sort=amount", []string{"Zq sort small", "Zq sort big", "Zq sort open"}},
		{"&sort=amount:desc", []string{"Zq sort open", "Zq sort big", "Zq sort small"}},
	} {
		var names []string
		for _, b := range decodeList[Budget](t, api.call(http.MethodGet, "/budgets?search=zq%20sort"+c.sort, nil)).Data {
			names = append(names, b.Name)
		}
		if !slices.Equal(names, c.want) {
			t.Errorf("GET ?search=zq sort%s = %v, want %v", c.sort, names, c.want)
		}
	}
}

func TestBudgets_EachRouteTakesExactlyItsScopes(t *testing.T) {
	api := newBudgetAPI(t)
	one := "/budgets/" + missingID
	all := []string{rbac.ScopeBudgetCreate, rbac.ScopeBudgetRead, rbac.ScopeBudgetUpdate, rbac.ScopeBudgetDelete, rbac.ScopeBudgetAssign, rbac.ScopeRBACRead}
	for i, route := range []struct {
		method, path string
		body         any
		scopes       []string
	}{
		{http.MethodGet, "/budgets", nil, []string{rbac.ScopeBudgetRead}},
		{http.MethodPost, "/budgets", nil, []string{rbac.ScopeBudgetCreate}},
		{http.MethodGet, one, nil, []string{rbac.ScopeBudgetRead}},
		{http.MethodPatch, one, map[string]any{"amount": "2"}, []string{rbac.ScopeBudgetUpdate}},
		{http.MethodDelete, one, nil, []string{rbac.ScopeBudgetDelete, rbac.ScopeBudgetAssign}},
		{http.MethodPut, one + "/makeDefault", nil, []string{rbac.ScopeBudgetAssign}},
	} {
		body := route.body
		if route.method == http.MethodPost {
			body = map[string]any{"name": "Zq gated " + strconv.Itoa(i), "subjectType": "user", "amount": "1"}
		}
		for _, missing := range route.scopes {
			others := slices.DeleteFunc(slices.Clone(all), func(s string) bool { return s == missing })
			if code := api.as(others...).call(route.method, route.path, body).Code; code != http.StatusForbidden {
				t.Errorf("%s %s without %s = %d, want 403", route.method, route.path, missing, code)
			}
		}
		if code := api.as(route.scopes...).call(route.method, route.path, body).Code; code == http.StatusForbidden || code == http.StatusUnauthorized {
			t.Errorf("%s %s with only %v = %d, want the gate to pass", route.method, route.path, route.scopes, code)
		}
	}
}

func TestBudgets_NameTakenIgnoresCase(t *testing.T) {
	api := newBudgetAPI(t)
	api.createBudget("Zq Case Team", "user", "1")
	expectMessage(t, api.call(http.MethodPost, "/budgets", map[string]any{"name": "zq case TEAM", "subjectType": "user", "amount": "1"}),
		http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictNameTaken])
}

func TestBudgets_AFormerDefaultIsDeletable(t *testing.T) {
	api := newBudgetAPI(t)
	expectStatus(t, api.call(http.MethodDelete, "/budgets/"+store.SeededDefaultUserBudgetID, nil), http.StatusConflict)
	successor := api.createBudget("Zq successor", "user", "1")
	expectStatus(t, api.call(http.MethodPut, "/budgets/"+successor.ID+"/makeDefault", nil), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, "/budgets/"+store.SeededDefaultUserBudgetID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/budgets/"+store.SeededDefaultUserBudgetID, nil), http.StatusNotFound)
}

func TestBudgets_PatchBindsAMiscasedKey(t *testing.T) {
	api := newBudgetAPI(t)
	b := api.createBudget("Zq miscased patch", "user", "10")
	rec := api.call(http.MethodPatch, "/budgets/"+b.ID, `{"Amount":"5"}`)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Budget](t, rec); got.Amount == nil || *got.Amount != "5.00" {
		t.Errorf("amount after a miscased PATCH = %v, want 5.00", got.Amount)
	}
}

func TestBudgets_WritesIgnoreAQueryString(t *testing.T) {
	api := newBudgetAPI(t)
	b := expectCreated[Budget](t, api.call(http.MethodPost, "/budgets?unread=1", map[string]any{"name": "Zq query write", "subjectType": "user", "amount": "1"}), "budgets")
	expectStatus(t, api.call(http.MethodPatch, "/budgets/"+b.ID+"?unread=1", map[string]any{"amount": "2"}), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, "/budgets/"+b.ID+"?unread=1", nil), http.StatusNoContent)
}

func TestBudgets_ReportsTheFirstPartWithAProblem(t *testing.T) {
	api := newBudgetAPI(t)
	rec := api.call(http.MethodGet, "/budgets?pageSize=0&sort=colour", nil)
	expectDetail(t, rec, http.StatusUnprocessableEntity, "pageSize")
	if details := decodeError(t, rec).ValidationDetails; len(details) != 1 {
		t.Errorf("validationDetails = %v, want pageSize alone", details)
	}
	rec = api.call(http.MethodPost, "/budgets", `{"name":"","subjectType":"user","amount":5}`)
	expectDetail(t, rec, http.StatusBadRequest, "amount")
	if details := decodeError(t, rec).ValidationDetails; len(details) != 1 {
		t.Errorf("validationDetails = %v, want amount alone", details)
	}
}
