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
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (a *entrypointAPI) createRole(name string, inherits ...string) Role {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/rbac/roles", map[string]any{
		"name": name, "description": "Role the test creates.",
		"scopes": []string{rbac.ScopeAgentBuilderRead}, "inherits": nonNilList(inherits),
	})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[Role](a.t, rec)
	expectLocation(a.t, rec, "/rbac/roles/"+created.ID)
	return created
}

func roleNames(roles []Role) []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = r.Name
	}
	return out
}

func TestRoles_Lifecycle(t *testing.T) {
	api := newRBACAPI(t)
	created := api.createRole("Support operator", builtinRoleID)

	if created.Type != "role" || created.Origin != entplatform.RBACOriginCustom {
		t.Errorf("type %q origin %q, want role and custom", created.Type, created.Origin)
	}
	if !slices.Equal(created.Inherits, []string{builtinRoleID}) || !slices.Equal(created.Scopes, []string{rbac.ScopeAgentBuilderRead}) {
		t.Errorf("inherits %v scopes %v", created.Inherits, created.Scopes)
	}
	if created.CreatedBy == nil || *created.CreatedBy == "" {
		t.Error("createdBy is null on a role created through the API")
	}
	raw := rawData(t, api.call(http.MethodGet, "/rbac/roles/"+created.ID, nil))
	for _, field := range []string{"createdTime", "updatedTime"} {
		if s, _ := raw[field].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", field, raw[field])
		}
	}
	for _, v1Only := range []string{"createdAt", "updatedAt"} {
		if _, ok := raw[v1Only]; ok {
			t.Errorf("v1 field %s is on the v2 role", v1Only)
		}
	}

	rec := api.call(http.MethodPut, "/rbac/roles/"+created.ID, map[string]any{"name": "Support operator 2"})
	expectStatus(t, rec, http.StatusOK)
	replaced := decodeData[Role](t, rec)
	if replaced.Name != "Support operator 2" || replaced.Description != nil || len(replaced.Scopes) != 0 || len(replaced.Inherits) != 0 {
		t.Errorf("replace kept omitted fields: %+v", replaced)
	}
	raw = rawData(t, rec)
	if v, ok := raw["description"]; !ok || v != nil {
		t.Errorf("cleared description = %v (present %v), want null", v, ok)
	}

	page := decodeList[Role](t, api.call(http.MethodGet, "/rbac/roles?origin=custom", nil))
	if !slices.Contains(roleNames(page.Data), "Support operator 2") {
		t.Errorf("list %v lacks the replaced role", roleNames(page.Data))
	}

	expectStatus(t, api.call(http.MethodDelete, "/rbac/roles/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/rbac/roles/"+created.ID, nil), http.StatusNotFound)
}

func TestRoles_YAMLRolesAreAddressableAndReadOnly(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	viewerID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")
	operatorID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_operator")

	rec := api.call(http.MethodGet, "/rbac/roles?origin=yaml", nil)
	expectStatus(t, rec, http.StatusOK)
	page := decodeList[Role](t, rec)
	if got := roleNames(page.Data); !slices.Equal(got, []string{"yaml_operator", "yaml_viewer"}) {
		t.Fatalf("yaml roles %v, want yaml_operator then yaml_viewer", got)
	}
	operator := page.Data[0]
	if operator.ID != operatorID || !slices.Equal(operator.Inherits, []string{viewerID}) {
		t.Errorf("operator id %s inherits %v, want %s inheriting %s", operator.ID, operator.Inherits, operatorID, viewerID)
	}
	if operator.CreatedBy != nil || operator.CreatedTime != 0 {
		t.Errorf("a YAML role has audit fields: %+v", operator)
	}

	for _, id := range []string{viewerID, operatorID} {
		got := api.call(http.MethodGet, "/rbac/roles/"+id, nil)
		expectStatus(t, got, http.StatusOK)
		raw := rawData(t, got)
		if raw["id"] != id || raw["origin"] != entplatform.RBACOriginYAML {
			t.Errorf("GET %s = id %v origin %v", id, raw["id"], raw["origin"])
		}
		for _, field := range []string{"createdBy", "updatedBy", "createdTime", "updatedTime"} {
			if v, ok := raw[field]; !ok || v != nil {
				t.Errorf("%s on a YAML role = %v (present %v), want null", field, v, ok)
			}
		}
	}

	const immutable = "System roles are defined in your configuration; they cannot be modified or deleted through the API"
	expectMessage(t, api.call(http.MethodPut, "/rbac/roles/"+viewerID, map[string]any{"name": "renamed"}), http.StatusConflict, immutable)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/roles/"+viewerID, nil), http.StatusConflict, immutable)
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles/"+missingID, nil), http.StatusNotFound)

	rec = api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "custom child", "inherits": []string{viewerID}})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "inherits")
	expectStatus(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "yaml_viewer"}), http.StatusConflict)
}

func TestRoles_BuiltinAndDefaultRolesAre409(t *testing.T) {
	api := newRBACAPI(t)
	const builtin = "Built-in role shipped and maintained by Solace; it cannot be modified or deleted through the API"
	expectMessage(t, api.call(http.MethodPut, "/rbac/roles/"+builtinRoleID, map[string]any{"name": "renamed"}), http.StatusConflict, builtin)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/roles/"+builtinRoleID, nil), http.StatusConflict, builtin)

	role := api.createRole("Everyone")
	expectStatus(t, api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{role.ID}}), http.StatusOK)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/roles/"+role.ID, nil), http.StatusConflict,
		"Assigned as a default role; remove it from the default roles before deleting it")
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles/"+role.ID, nil), http.StatusOK)
}

func TestRoles_Paging(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	for _, name := range []string{"r-a", "r-b", "r-c"} {
		api.createRole(name)
	}
	names := func(query string) ([]string, entplatform.PaginationMeta) {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/roles?"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		page := decodeList[Role](t, rec)
		return roleNames(page.Data), page.Meta.Pagination
	}

	if got, m := names("origin=custom&pageSize=2"); m.Count != 3 || m.NextPage == nil || *m.NextPage != 2 || !slices.Equal(got, []string{"r-a", "r-b"}) {
		t.Errorf("custom page 1 = %v with %+v, want [r-a r-b] of 3 with nextPage 2", got, m)
	}
	got, m := names("origin=custom&pageSize=2&pageNumber=2")
	if !slices.Equal(got, []string{"r-c"}) {
		t.Errorf("custom page 2 = %v, want [r-c]", got)
	}
	if m.NextPage != nil {
		t.Errorf("custom page 2 nextPage = %d, want null on the last page", *m.NextPage)
	}
	if got, _ := names("origin=custom&pageSize=2&pageNumber=3"); len(got) != 0 {
		t.Errorf("page past the end = %v, want []", got)
	}
	if got, _ := names("origin=custom&sort=name:desc"); !slices.Equal(got, []string{"r-c", "r-b", "r-a"}) {
		t.Errorf("name desc = %v", got)
	}
	if got, _ := names("origin=custom&sort=createdTime:desc"); !slices.Equal(got, []string{"r-c", "r-b", "r-a"}) {
		t.Errorf("createdTime desc = %v", got)
	}
	if got, _ := names("origin=custom&search=R-B"); !slices.Equal(got, []string{"r-b"}) {
		t.Errorf("search = %v", got)
	}

	_, builtins := names("origin=builtin&pageSize=1")
	all, total := names("pageSize=3")
	if total.Count != 2+3+builtins.Count {
		t.Errorf("total %d, want 2 YAML + 3 custom + %d built-in", total.Count, builtins.Count)
	}
	if !slices.Equal(all[:2], []string{"yaml_operator", "yaml_viewer"}) || slices.Contains([]string{"yaml_operator", "yaml_viewer"}, all[2]) {
		t.Errorf("first page %v, want the YAML roles then a database role", all)
	}
	straddle, _ := names("pageSize=1&pageNumber=3")
	if len(straddle) != 1 || straddle[0] != all[2] {
		t.Errorf("page 3 of 1 = %v, want %q, the first database role", straddle, all[2])
	}

	expectDetail(t, api.call(http.MethodGet, "/rbac/roles?origin=bogus", nil), http.StatusUnprocessableEntity, "origin")
	expectDetail(t, api.call(http.MethodGet, "/rbac/roles?sort=scopes:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/rbac/roles?sort=name:up", nil), http.StatusBadRequest, "sort")
	expectDetail(t, api.call(http.MethodGet, "/rbac/roles?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/rbac/roles?pageSize=ten", nil), http.StatusBadRequest, "pageSize")
}

func TestRoles_Validation(t *testing.T) {
	api := newRBACAPI(t)
	expectDetail(t, api.call(http.MethodGet, "/rbac/roles/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPut, "/rbac/roles/sam_manager", map[string]any{"name": "x"}), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPost, "/rbac/roles", `{"name": 1}`), http.StatusBadRequest, "name")
	expectDetail(t, api.call(http.MethodPost, "/rbac/roles", `{"name": "x", "colour": "red"}`), http.StatusBadRequest, "colour")
	expectStatus(t, api.call(http.MethodPost, "/rbac/roles", `{"name": "x"`), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{}), http.StatusUnprocessableEntity, "name")

	expectDetail(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "   ", "scopes": []string{rbac.ScopeAgentBuilderRead}}), http.StatusUnprocessableEntity, "name")

	for _, scope := range []string{"not a scope", "Sam:Agents:Read"} {
		rec := api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "bad scopes", "scopes": []string{scope}})
		expectStatus(t, rec, http.StatusUnprocessableEntity)
		if body := decodeError(t, rec); len(body.ValidationDetails) == 0 {
			t.Errorf("scope %q has no validationDetails: %s", scope, rec.Body.String())
		}
	}
	expectDetail(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "orphan", "inherits": []string{missingID}}), http.StatusUnprocessableEntity, "inherits")

	api.createRole("taken")
	expectStatus(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "taken"}), http.StatusConflict)
}

func TestRoles_Scopes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("scoped")
	reader := rbac.ScopeRBACRead

	expectStatus(t, api.call(http.MethodGet, "/rbac/roles", nil, reader), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles/"+role.ID, nil, reader), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "x"}, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodPut, "/rbac/roles/"+role.ID, map[string]any{"name": "x"}, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/roles/"+role.ID, nil, "rbac:*:*"), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "by superuser"}, "*:*:*"), http.StatusCreated)
}

func TestRoles_MiniIDPRefusesWrites(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles", nil), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "x"}), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodPut, "/rbac/roles/"+builtinRoleID, map[string]any{"name": "x"}), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/roles/"+builtinRoleID, nil), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}

func TestRoles_ReplaceRefusals(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("replace-refusals")
	path := "/rbac/roles/" + role.ID

	expectDetail(t, api.call(http.MethodPut, path, `{"name": 1}`), http.StatusBadRequest, "name")
	expectDetail(t, api.call(http.MethodPut, path, `{"name": "x", "colour": "red"}`), http.StatusBadRequest, "colour")
	expectStatus(t, api.call(http.MethodPut, path, `{"name": "x"`), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodPut, path, map[string]any{}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPut, path, map[string]any{"name": "x", "inherits": []string{missingID}}), http.StatusUnprocessableEntity, "inherits")
	expectStatus(t, api.call(http.MethodPut, "/rbac/roles/"+missingID, map[string]any{"name": "x"}), http.StatusNotFound)
}

func TestRoles_ReplaceKeepsNameConflict(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	shared := &store.RBACRole{Name: "yaml_viewer", CreatedBy: "seed", UpdatedBy: "seed"}
	if err := store.NewRBACRepo(api.core.DB()).CreateRole(t.Context(), shared, []string{rbac.ScopeAgentBuilderRead}, nil); err != nil {
		t.Fatalf("seed role sharing a configuration name: %v", err)
	}
	path := "/rbac/roles/" + shared.ID
	if got := decodeData[Role](t, api.call(http.MethodGet, path, nil)); !got.NameConflict {
		t.Fatalf("GET nameConflict = false, want true")
	}

	rec := api.call(http.MethodPut, path, map[string]any{"name": "yaml_viewer", "description": "Kept name.", "scopes": []string{rbac.ScopeAgentBuilderRead}})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Role](t, rec); !got.NameConflict {
		t.Errorf("PUT nameConflict = false, want true as GET reports")
	}
}
