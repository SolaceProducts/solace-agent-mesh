// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (a *entrypointAPI) rbacListRoles(path string) entplatform.PaginatedResponse[Role] {
	a.t.Helper()
	rec := a.call(http.MethodGet, path, nil)
	expectStatus(a.t, rec, http.StatusOK)
	return decodeList[Role](a.t, rec)
}

func TestRoles_SearchMatchesTheDescription(t *testing.T) {
	api := newRBACAPI(t)
	for _, r := range []struct{ name, description string }{
		{"sam_editor", "can edit things"},
		{"sam_viewer", "read only access"},
		{"billing_admin", "manage billing"},
	} {
		expectStatus(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{
			"name": r.name, "description": r.description, "scopes": []string{rbac.ScopeAgentBuilderRead},
		}), http.StatusCreated)
	}

	if got := roleNames(api.rbacListRoles("/rbac/roles?origin=custom&search=read").Data); !slices.Equal(got, []string{"sam_viewer"}) {
		t.Errorf("search=read = %v, want [sam_viewer], matched on its description", got)
	}
}

func TestRoles_InheritanceCycleIs422(t *testing.T) {
	api := newRBACAPI(t)
	a := api.createRole("role_a")
	b := api.createRole("role_b", a.ID)

	rec := api.call(http.MethodPut, "/rbac/roles/"+a.ID, map[string]any{
		"name": "role_a", "scopes": []string{rbac.ScopeAgentBuilderRead}, "inherits": []string{b.ID},
	})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "inherits")
}

func TestGrants_SortByState(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("state-role")
	pendingA := api.createGrant(emailSubject("ghost1@example.com"), role.ID)
	pendingB := api.createGrant(emailSubject("ghost2@example.com"), role.ID)
	concrete := api.createGrant(systemUserSubject(api.createSystemUser("state-bot", api.createRole("bot-role").ID)), role.ID)

	ids := func(query string) []string {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/grants?origin=custom&roleId="+role.ID+"&"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		return grantIDs(decodeList[Grant](t, rec).Data)
	}
	if got := ids("sort=state:asc"); len(got) != 3 || got[0] != concrete.ID {
		t.Errorf("state asc = %v, want the concrete grant %s first", got, concrete.ID)
	}
	if got := ids("sort=state:desc"); len(got) != 3 || got[2] != concrete.ID || !slices.Contains(got[:2], pendingA.ID) || !slices.Contains(got[:2], pendingB.ID) {
		t.Errorf("state desc = %v, want the pending grants then %s", got, concrete.ID)
	}
}

func TestClaimMappings_SortByProviderAndUpdatedTimeAndFoldNameCase(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	role := api.createRole("mapped_role")
	for _, m := range []struct{ name, provider, value string }{
		{"Eng Mapping", "azure", "eng"},
		{"Sales Mapping", "okta", "sales"},
		{"engineering overflow", "auth0", "eng2"},
	} {
		expectStatus(t, api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
			"name": m.name, "oidcProvider": m.provider, "claimValue": m.value, "roleIds": []string{role.ID},
		}), http.StatusCreated)
	}
	values := func(query string) []string {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/claimMappings?origin=custom&"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		return claimMappingValues(decodeList[ClaimMapping](t, rec).Data)
	}

	if got := values("sort=oidcProvider:asc"); !slices.Equal(got, []string{"eng2", "eng", "sales"}) {
		t.Errorf("oidcProvider asc = %v, want [eng2 eng sales]", got)
	}
	for value, updated := range map[string]int64{"sales": 1_000, "eng2": 2_000, "eng": 3_000} {
		if _, err := api.core.DB().ExecContext(t.Context(), `UPDATE rbac_idp_claim_mappings SET updated_time = $1 WHERE claim_value = $2`, updated, value); err != nil {
			t.Fatalf("set updated_time of %s: %v", value, err)
		}
	}
	if got := values("sort=updatedTime:desc"); !slices.Equal(got, []string{"eng", "eng2", "sales"}) {
		t.Errorf("updatedTime desc = %v, want [eng eng2 sales]", got)
	}

	rec := api.call(http.MethodGet, "/rbac/claimMappings?origin=custom&sort=name:asc", nil)
	expectStatus(t, rec, http.StatusOK)
	var names []string
	for _, m := range decodeList[ClaimMapping](t, rec).Data {
		names = append(names, *m.Name)
	}
	if want := []string{"Eng Mapping", "engineering overflow", "Sales Mapping"}; !slices.Equal(names, want) {
		t.Errorf("name asc = %v, want %v: a lowercase name sorts among the capitalised ones", names, want)
	}

	if got := values("roleId=" + missingID); len(got) != 0 {
		t.Errorf("roleId of a role nothing maps to = %v, want []", got)
	}
}

func TestClaimMappings_RoleFilterAndSearchReachConfiguredMappings(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML(
		rbac.DBClaimMapping{OIDCProvider: "azure", ClaimKey: testClaimKey, ClaimValue: "eng", RoleID: "yaml_viewer"},
	)))
	dbRole := api.createRole("db_role")
	api.createClaimMapping("Sales Mapping", "sales", dbRole.ID)

	list := func(query string) []ClaimMapping {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/claimMappings?"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		return decodeList[ClaimMapping](t, rec).Data
	}

	if got := list("roleId=" + entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")); len(got) != 1 || got[0].Origin != entplatform.RBACOriginYAML {
		t.Errorf("roleId of the configured role = %v, want the one configured mapping", claimMappingValues(got))
	}
	if got := list("search=eng"); len(got) != 1 || got[0].Origin != entplatform.RBACOriginYAML {
		t.Errorf("search=eng = %v, want the configured mapping matched on its claim value", claimMappingValues(got))
	}
	if got := list("roleId=" + dbRole.ID); len(got) != 1 || got[0].ClaimValue != "sales" {
		t.Errorf("roleId of the database role = %v, want [sales]", claimMappingValues(got))
	}
}

func TestSystemUsers_ListOffersOnlyTheDefaultBuiltin(t *testing.T) {
	api := newRBACAPI(t)
	if got := systemUserNames(decodeList[ServiceAccount](t, api.call(http.MethodGet, "/rbac/serviceAccounts?pageSize=100", nil)).Data); !slices.Equal(got, []string{"default"}) {
		t.Errorf("baseline = %v, want [default]: the channel and eval system users are hidden", got)
	}
	api.createSystemUser("helpdesk", api.createRole("orchestrator_only").ID)
	if got := systemUserNames(decodeList[ServiceAccount](t, api.call(http.MethodGet, "/rbac/serviceAccounts?search=helpdesk", nil)).Data); !slices.Equal(got, []string{"helpdesk"}) {
		t.Errorf("search=helpdesk = %v, want [helpdesk]", got)
	}
}

func TestRBACLists_RefuseMalformedSortAndPage(t *testing.T) {
	api := newRBACAPI(t)
	for _, path := range []string{"/rbac/roles", "/rbac/scopes", "/rbac/grants", "/rbac/claimMappings", "/rbac/systemUsers", "/users"} {
		for _, c := range []struct {
			query, field string
			status       int
		}{
			{"sort=name:sideways", "sort", http.StatusBadRequest},
			{"sort=:asc", "sort", http.StatusBadRequest},
			{"pageSize=0", "pageSize", http.StatusUnprocessableEntity},
			{"pageNumber=99999999999999999&pageSize=100", "pageNumber", http.StatusUnprocessableEntity},
		} {
			rec := api.call(http.MethodGet, path+"?"+c.query, nil)
			if rec.Code != c.status || len(decodeError(t, rec).ValidationDetails[c.field]) == 0 {
				t.Errorf("GET %s?%s = %d %s, want %d naming %s", path, c.query, rec.Code, rec.Body.String(), c.status, c.field)
			}
		}
		rec := api.call(http.MethodGet, path+"?pageNumber=1000000&pageSize=100", nil)
		expectStatus(t, rec, http.StatusOK)
		if got := decodeList[map[string]any](t, rec).Data; len(got) != 0 {
			t.Errorf("GET %s at the last allowed page = %d rows, want an empty page", path, len(got))
		}
	}
}
