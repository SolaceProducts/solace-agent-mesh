// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func scopeStrings(entries []ScopeCatalogEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Scope
	}
	return out
}

func TestScopes_ListPagingAndFilters(t *testing.T) {
	api := newRBACAPI(t)
	list := func(query string) ([]ScopeCatalogEntry, int) {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/scopes?"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		page := decodeList[ScopeCatalogEntry](t, rec)
		return page.Data, page.Meta.Pagination.Count
	}

	all, total := list("pageSize=100")
	if want := len(rbac.Catalog()); total != want {
		t.Fatalf("total %d, want %d catalog scopes", total, want)
	}
	if !slices.IsSorted(scopeStrings(all)) {
		t.Errorf("default order is not scope ascending: %v", scopeStrings(all))
	}

	byName, _ := list("pageSize=100&sort=name:desc")
	for i := 1; i < len(byName); i++ {
		if strings.ToLower(byName[i-1].Name) < strings.ToLower(byName[i].Name) {
			t.Fatalf("name desc out of order at %d: %q before %q", i, byName[i-1].Name, byName[i].Name)
		}
	}

	agents, agentTotal := list("pageSize=100&category=agent")
	if agentTotal == 0 || agentTotal >= total {
		t.Errorf("category=agent matched %d of %d", agentTotal, total)
	}
	for _, e := range agents {
		if !strings.HasPrefix(e.Scope, "agent:") {
			t.Errorf("category=agent returned %q", e.Scope)
		}
	}

	found, foundTotal := list("pageSize=100&search=INVOKE")
	if foundTotal == 0 || foundTotal >= total {
		t.Errorf("search matched %d of %d", foundTotal, total)
	}
	for _, e := range found {
		if !strings.Contains(strings.ToLower(e.Scope+" "+e.Name), "invoke") {
			t.Errorf("search=INVOKE returned %+v", e)
		}
	}

	first, _ := list("pageSize=5")
	second, _ := list("pageSize=5&pageNumber=2")
	if len(first) != 5 || len(second) == 0 || first[0].Scope == second[0].Scope {
		t.Errorf("page 1 %v and page 2 %v should differ", scopeStrings(first), scopeStrings(second))
	}
	if past, _ := list("pageSize=5&pageNumber=1000"); len(past) != 0 {
		t.Errorf("page past the end = %v, want []", scopeStrings(past))
	}

	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(api.call(http.MethodGet, "/rbac/scopes", nil).Body.Bytes(), &raw); err != nil || len(raw.Data) == 0 {
		t.Fatalf("decode raw list: %v", err)
	}
	for _, key := range []string{"scope", "name", "description", "resourceName"} {
		if _, ok := raw.Data[0][key]; !ok {
			t.Errorf("entry lacks key %q: %v", key, raw.Data[0])
		}
	}
	if raw.Data[0]["resourceName"] != nil {
		t.Errorf("resourceName on a template = %v, want null", raw.Data[0]["resourceName"])
	}

	expectDetail(t, api.call(http.MethodGet, "/rbac/scopes?expand=bogus", nil), http.StatusUnprocessableEntity, "expand")
	expectDetail(t, api.call(http.MethodGet, "/rbac/scopes?sort=bogus:asc", nil), http.StatusUnprocessableEntity, "sort")
}

func TestScopes_ExpandResourceScopes(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "Scope catalog agent")

	base := decodeList[ScopeCatalogEntry](t, api.call(http.MethodGet, "/rbac/scopes?pageSize=100", nil))
	rec := api.call(http.MethodGet, "/rbac/scopes?pageSize=100&expand=resourceScopes&search=Scope%20catalog%20agent", nil)
	expectStatus(t, rec, http.StatusOK)
	expanded := decodeList[ScopeCatalogEntry](t, rec)
	if len(expanded.Data) == 0 {
		t.Fatalf("expand=resourceScopes found no entry for agent %s", agent.ID)
	}
	for _, e := range expanded.Data {
		if e.ResourceName == nil || *e.ResourceName != "Scope catalog agent" || strings.Contains(e.Scope, "*") {
			t.Errorf("per-instance entry %+v", e)
		}
	}

	all := decodeList[ScopeCatalogEntry](t, api.call(http.MethodGet, "/rbac/scopes?pageSize=1&expand=resourceScopes", nil))
	if all.Meta.Pagination.Count < base.Meta.Pagination.Count+len(expanded.Data) {
		t.Errorf("expanded total %d, want at least %d base + %d instance scopes",
			all.Meta.Pagination.Count, base.Meta.Pagination.Count, len(expanded.Data))
	}
}

func TestScopes_RBACFlagOffIs404(t *testing.T) {
	t.Setenv(features.EnvVarName("rbac"), "false")
	api := newRBACAPI(t)
	expectStatus(t, api.call(http.MethodGet, "/rbac/scopes", nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles", nil), http.StatusOK)
}

func TestScopes_Scopes(t *testing.T) {
	api := newRBACAPI(t)
	expectStatus(t, api.call(http.MethodGet, "/rbac/scopes", nil, rbac.ScopeRBACRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/rbac/scopes", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
}
