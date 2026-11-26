// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"regexp"
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	testProvider = "okta"
	testClaimKey = "groups"
)

func claimMappingYAML(mappings ...rbac.DBClaimMapping) rbac.YAMLState {
	state := testRBACYAML
	state.ClaimOIDCProvider = testProvider
	state.ClaimKey = testClaimKey
	state.ClaimMappings = mappings
	return state
}

func (a *entrypointAPI) createClaimMapping(name, value string, roleIDs ...string) ClaimMapping {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": name, "claimValue": value, "roleIds": roleIDs,
	})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[ClaimMapping](a.t, rec)
	expectLocation(a.t, rec, "/rbac/claimMappings/"+created.ID)
	return created
}

func claimMappingValues(mappings []ClaimMapping) []string {
	out := make([]string, len(mappings))
	for i, m := range mappings {
		out[i] = m.ClaimValue
	}
	return out
}

func TestClaimMappings_Lifecycle(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	role := api.createRole("mapped")
	other := api.createRole("mapped too")

	rec := api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "Engineers", "claimValue": "engineers", "roleIds": []string{role.ID},
	})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[ClaimMapping](t, rec)
	expectLocation(t, rec, "/rbac/claimMappings/"+created.ID)
	if created.Type != "claimMapping" || created.Origin != entplatform.RBACOriginCustom {
		t.Errorf("type %q origin %q, want claimMapping and custom", created.Type, created.Origin)
	}
	if created.Name == nil || *created.Name != "Engineers" || created.ClaimValue != "engineers" {
		t.Errorf("name %v value %q", created.Name, created.ClaimValue)
	}
	if orEmpty(created.OIDCProvider) != testProvider || orEmpty(created.ClaimKey) != testClaimKey {
		t.Errorf("omitted provider %q key %q, want the deployment's %q and %q", orEmpty(created.OIDCProvider), orEmpty(created.ClaimKey), testProvider, testClaimKey)
	}
	if !slices.Equal(created.RoleIDs, []string{role.ID}) {
		t.Errorf("roleIds %v, want [%s]", created.RoleIDs, role.ID)
	}
	if created.CreatedBy == nil || *created.CreatedBy == "" {
		t.Error("createdBy is null on a mapping created through the API")
	}
	raw := rawData(t, rec)
	for _, field := range []string{"createdTime", "updatedTime"} {
		if s, _ := raw[field].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", field, raw[field])
		}
	}
	for _, v1Only := range []string{"createdAt", "updatedAt"} {
		if _, ok := raw[v1Only]; ok {
			t.Errorf("v1 field %s is on the v2 claim mapping", v1Only)
		}
	}

	rec = api.call(http.MethodGet, "/rbac/claimMappings/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[ClaimMapping](t, rec); got.ID != created.ID || got.ClaimValue != "engineers" {
		t.Errorf("GET = %+v", got)
	}

	rec = api.call(http.MethodPut, "/rbac/claimMappings/"+created.ID, map[string]any{
		"name": "Engineers 2", "claimValue": "engineers-2", "roleIds": []string{role.ID, other.ID},
	})
	expectStatus(t, rec, http.StatusOK)
	replaced := decodeData[ClaimMapping](t, rec)
	if replaced.ID != created.ID || replaced.Name == nil || *replaced.Name != "Engineers 2" || replaced.ClaimValue != "engineers-2" {
		t.Errorf("replace = %+v", replaced)
	}
	if orEmpty(replaced.OIDCProvider) != testProvider {
		t.Errorf("replace without oidcProvider gave %q, want the stored %q", orEmpty(replaced.OIDCProvider), testProvider)
	}
	if len(replaced.RoleIDs) != 2 || !slices.Contains(replaced.RoleIDs, role.ID) || !slices.Contains(replaced.RoleIDs, other.ID) {
		t.Errorf("replaced roleIds %v", replaced.RoleIDs)
	}
	if replaced.UpdatedTime == 0 || replaced.CreatedTime == 0 {
		t.Errorf("replace response lacks audit times: %+v", replaced)
	}

	page := decodeList[ClaimMapping](t, api.call(http.MethodGet, "/rbac/claimMappings?origin=custom", nil))
	if !slices.Equal(claimMappingValues(page.Data), []string{"engineers-2"}) {
		t.Errorf("list %v lacks the replaced mapping", claimMappingValues(page.Data))
	}

	expectStatus(t, api.call(http.MethodDelete, "/rbac/claimMappings/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/rbac/claimMappings/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/rbac/claimMappings/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPut, "/rbac/claimMappings/"+created.ID, map[string]any{
		"name": "x", "claimValue": "x", "roleIds": []string{role.ID},
	}), http.StatusNotFound)
}

func TestClaimMappings_ReplaceResetsOmittedProviderAndClaimKey(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	role := api.createRole("mapped")
	rec := api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "Custom", "oidcProvider": "auth0", "claimKey": "roles", "claimValue": "custom", "roleIds": []string{role.ID},
	})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[ClaimMapping](t, rec)
	if orEmpty(created.ClaimKey) != "roles" || orEmpty(created.OIDCProvider) != "auth0" {
		t.Fatalf("created provider %q key %q, want auth0 and roles", orEmpty(created.OIDCProvider), orEmpty(created.ClaimKey))
	}

	rec = api.call(http.MethodPut, "/rbac/claimMappings/"+created.ID, map[string]any{
		"name": "Custom", "claimValue": "custom", "roleIds": []string{role.ID},
	})
	expectStatus(t, rec, http.StatusOK)
	replaced := decodeData[ClaimMapping](t, rec)
	if orEmpty(replaced.OIDCProvider) != testProvider {
		t.Errorf("provider %q, want the deployment's %q", orEmpty(replaced.OIDCProvider), testProvider)
	}
	if orEmpty(replaced.ClaimKey) != testClaimKey {
		t.Errorf("claimKey %q, want the deployment's %q", orEmpty(replaced.ClaimKey), testClaimKey)
	}
}

func TestClaimMappings_YAMLMappingsAreAddressableAndReadOnly(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML(
		rbac.DBClaimMapping{OIDCProvider: testProvider, ClaimKey: testClaimKey, ClaimValue: "ops", RoleID: "yaml_viewer"},
		rbac.DBClaimMapping{OIDCProvider: testProvider, ClaimKey: testClaimKey, ClaimValue: "ops", RoleID: "yaml_operator"},
	)))
	viewerID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")
	operatorID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_operator")
	id := entplatform.YAMLRowID(entplatform.YAMLKindClaimMapping, testProvider+"|ops")

	page := decodeList[ClaimMapping](t, api.call(http.MethodGet, "/rbac/claimMappings?origin=yaml", nil))
	if len(page.Data) != 1 {
		t.Fatalf("yaml mappings %+v, want one", page.Data)
	}
	got := page.Data[0]
	if got.ID != id || got.Origin != entplatform.RBACOriginYAML || orEmpty(got.OIDCProvider) != testProvider || orEmpty(got.ClaimKey) != testClaimKey {
		t.Errorf("yaml mapping = %+v, want id %s", got, id)
	}
	if !slices.Equal(got.RoleIDs, []string{viewerID, operatorID}) {
		t.Errorf("roleIds %v, want the YAML roles' v2 ids [%s %s]", got.RoleIDs, viewerID, operatorID)
	}

	rec := api.call(http.MethodGet, "/rbac/claimMappings/"+id, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	if raw["id"] != id || raw["type"] != "claimMapping" {
		t.Errorf("GET = id %v type %v", raw["id"], raw["type"])
	}
	for _, field := range []string{"name", "createdBy", "updatedBy", "createdTime", "updatedTime"} {
		if v, ok := raw[field]; !ok || v != nil {
			t.Errorf("%s on a YAML mapping = %v (present %v), want null", field, v, ok)
		}
	}

	body := map[string]any{"name": "x", "claimValue": "ops", "roleIds": []string{viewerID}}
	expectMessage(t, api.call(http.MethodPut, "/rbac/claimMappings/"+id, body), http.StatusConflict, msgYAMLClaimMappingImmutable)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/claimMappings/"+id, nil), http.StatusConflict, msgYAMLClaimMappingImmutable)
	expectStatus(t, api.call(http.MethodGet, "/rbac/claimMappings/"+missingID, nil), http.StatusNotFound)
}

func TestClaimMappings_TakenValueIs409(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	role := api.createRole("taken-by")
	api.createClaimMapping("first", "dup", role.ID)
	expectStatus(t, api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "second", "claimValue": "dup", "roleIds": []string{role.ID},
	}), http.StatusConflict)

	other := api.createClaimMapping("other", "other", role.ID)
	expectStatus(t, api.call(http.MethodPut, "/rbac/claimMappings/"+other.ID, map[string]any{
		"name": "other", "claimValue": "dup", "roleIds": []string{role.ID},
	}), http.StatusConflict)
}

func TestClaimMappings_ConfigurationClashIs409(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML(
		rbac.DBClaimMapping{OIDCProvider: testProvider, ClaimKey: testClaimKey, ClaimValue: "ops", RoleID: "mapped-by-config"},
	)))
	role := api.createRole("mapped-by-config")
	const clash = "Claim mapping (" + testProvider + "=ops → mapped-by-config) is defined in your configuration"

	expectMessage(t, api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "shadow", "claimValue": "ops", "roleIds": []string{role.ID},
	}), http.StatusConflict, clash)

	existing := api.createClaimMapping("elsewhere", "dev", role.ID)
	expectMessage(t, api.call(http.MethodPut, "/rbac/claimMappings/"+existing.ID, map[string]any{
		"name": "elsewhere", "claimValue": "ops", "roleIds": []string{role.ID},
	}), http.StatusConflict, clash)
}

func TestClaimMappings_Paging(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML(
		rbac.DBClaimMapping{OIDCProvider: testProvider, ClaimKey: testClaimKey, ClaimValue: "ops", RoleID: "yaml_viewer"},
	)))
	roleA := api.createRole("role-a")
	roleB := api.createRole("role-b")
	api.createClaimMapping("m-a", "va", roleA.ID)
	api.createClaimMapping("m-b", "vb", roleB.ID)
	api.createClaimMapping("m-c", "vc", roleA.ID)
	expectStatus(t, api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "m-d", "oidcProvider": "auth0", "claimValue": "vd", "roleIds": []string{roleA.ID},
	}), http.StatusCreated)

	values := func(query string) ([]string, int) {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/claimMappings?"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		page := decodeList[ClaimMapping](t, rec)
		return claimMappingValues(page.Data), page.Meta.Pagination.Count
	}

	if got, total := values("origin=custom&pageSize=3"); total != 4 || !slices.Equal(got, []string{"va", "vb", "vc"}) {
		t.Errorf("custom page 1 = %v of %d, want [va vb vc] of 4", got, total)
	}
	if got, _ := values("origin=custom&pageSize=3&pageNumber=2"); !slices.Equal(got, []string{"vd"}) {
		t.Errorf("custom page 2 = %v, want [vd]", got)
	}
	if got, _ := values("origin=custom&pageSize=3&pageNumber=3"); len(got) != 0 {
		t.Errorf("page past the end = %v, want []", got)
	}
	if got, _ := values("origin=yaml"); !slices.Equal(got, []string{"ops"}) {
		t.Errorf("origin=yaml = %v", got)
	}
	if got, total := values("pageSize=1"); total != 5 || !slices.Equal(got, []string{"ops"}) {
		t.Errorf("all page 1 = %v of %d, want the YAML mapping first of 5", got, total)
	}
	if got, _ := values("pageSize=2&pageNumber=2"); !slices.Equal(got, []string{"vb", "vc"}) {
		t.Errorf("all page 2 = %v, want [vb vc]: the database rows from offset 1, past the YAML mapping", got)
	}
	if got, _ := values("pageSize=2&pageNumber=3"); !slices.Equal(got, []string{"vd"}) {
		t.Errorf("all page 3 = %v, want [vd]", got)
	}
	if got, _ := values("origin=custom&oidcProvider=auth0"); !slices.Equal(got, []string{"vd"}) {
		t.Errorf("oidcProvider = %v", got)
	}
	if got, _ := values("origin=custom&roleId=" + roleB.ID); !slices.Equal(got, []string{"vb"}) {
		t.Errorf("roleId = %v", got)
	}
	if got, _ := values("origin=custom&search=M-C"); !slices.Equal(got, []string{"vc"}) {
		t.Errorf("search = %v", got)
	}
	if got, _ := values("origin=custom&sort=name:desc"); !slices.Equal(got, []string{"vd", "vc", "vb", "va"}) {
		t.Errorf("name desc = %v", got)
	}
	if got, _ := values("origin=custom&sort=claimValue:desc"); !slices.Equal(got, []string{"vd", "vc", "vb", "va"}) {
		t.Errorf("claimValue desc = %v", got)
	}
	for value, createdTime := range map[string]int64{"vc": 1_000, "va": 2_000, "vd": 3_000, "vb": 4_000} {
		if _, err := api.core.DB().ExecContext(t.Context(), `UPDATE rbac_idp_claim_mappings SET created_time = $1 WHERE claim_value = $2`, createdTime, value); err != nil {
			t.Fatalf("set created_time of %s: %v", value, err)
		}
	}
	if got, _ := values("origin=custom&sort=createdTime:asc"); !slices.Equal(got, []string{"vc", "va", "vd", "vb"}) {
		t.Errorf("createdTime asc = %v, want [vc va vd vb]", got)
	}
	if got, _ := values("origin=custom&sort=createdTime:desc"); !slices.Equal(got, []string{"vb", "vd", "va", "vc"}) {
		t.Errorf("createdTime desc = %v, want [vb vd va vc]", got)
	}

	expectDetail(t, api.call(http.MethodGet, "/rbac/claimMappings?origin=bogus", nil), http.StatusUnprocessableEntity, "origin")
	expectDetail(t, api.call(http.MethodGet, "/rbac/claimMappings?roleId=not-a-uuid", nil), http.StatusBadRequest, "roleId")
	expectDetail(t, api.call(http.MethodGet, "/rbac/claimMappings?sort=roleIds:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/rbac/claimMappings?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
}

func TestClaimMappings_Validation(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	role := api.createRole("mapped")
	valid := func(over map[string]any) map[string]any {
		body := map[string]any{"name": "n", "claimValue": "v", "roleIds": []string{role.ID}}
		for k, v := range over {
			body[k] = v
		}
		return body
	}

	expectDetail(t, api.call(http.MethodGet, "/rbac/claimMappings/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPut, "/rbac/claimMappings/not-a-uuid", valid(nil)), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodDelete, "/rbac/claimMappings/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodPost, "/rbac/claimMappings", `{"name": "x"`), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodPost, "/rbac/claimMappings", valid(map[string]any{"colour": "red"})), http.StatusBadRequest, "colour")
	expectDetail(t, api.call(http.MethodPost, "/rbac/claimMappings", `{"name": 1}`), http.StatusBadRequest, "name")

	rec := api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{"roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	if len(details["name"]) == 0 || len(details["claimValue"]) == 0 {
		t.Errorf("a body missing name and claimValue reports %v, want both", details)
	}
	expectDetail(t, api.call(http.MethodPost, "/rbac/claimMappings", valid(map[string]any{"roleIds": []string{}})), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPost, "/rbac/claimMappings", valid(map[string]any{"claimValue": ""})), http.StatusUnprocessableEntity, "claimValue")
	expectDetail(t, api.call(http.MethodPost, "/rbac/claimMappings", valid(map[string]any{"roleIds": []string{missingID}})), http.StatusUnprocessableEntity, "roleIds")
	yamlRole := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")
	expectDetail(t, api.call(http.MethodPost, "/rbac/claimMappings", valid(map[string]any{"roleIds": []string{yamlRole}})), http.StatusUnprocessableEntity, "roleIds")

	created := api.createClaimMapping("exists", "exists", role.ID)
	expectDetail(t, api.call(http.MethodPut, "/rbac/claimMappings/"+created.ID, valid(map[string]any{"roleIds": []string{missingID}})), http.StatusUnprocessableEntity, "roleIds")
}

func TestClaimMappings_Scopes(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	role := api.createRole("scoped")
	mapping := api.createClaimMapping("scoped", "scoped", role.ID)
	reader := rbac.ScopeRBACRead
	body := map[string]any{"name": "x", "claimValue": "x", "roleIds": []string{role.ID}}

	expectStatus(t, api.call(http.MethodGet, "/rbac/claimMappings", nil, reader), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/rbac/claimMappings/"+mapping.ID, nil, reader), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/claimMappings", body, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodPut, "/rbac/claimMappings/"+mapping.ID, body, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/claimMappings/"+mapping.ID, nil, "rbac:*:*"), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodGet, "/rbac/claimMappings", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "by superuser", "claimValue": "su", "roleIds": []string{role.ID},
	}, "*:*:*"), http.StatusCreated)
}

func TestClaimMappings_MiniIDPRefusesWrites(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	body := map[string]any{"name": "x", "claimValue": "x", "roleIds": []string{builtinRoleID}}
	expectStatus(t, api.call(http.MethodGet, "/rbac/claimMappings", nil), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/claimMappings", body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodPut, "/rbac/claimMappings/"+missingID, body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/claimMappings/"+missingID, nil), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}
