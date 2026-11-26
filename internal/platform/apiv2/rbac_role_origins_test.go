// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (a *entrypointAPI) rbacRename(roleID, name string) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.call(http.MethodPut, "/rbac/roles/"+roleID, map[string]any{"name": name, "scopes": []string{rbac.ScopeAgentBuilderRead}})
}

func TestRoles_RenameThatMovesAConfigurationReferenceIs409(t *testing.T) {
	referenced := rbac.YAMLState{ClaimMappings: []rbac.DBClaimMapping{{OIDCProvider: "azure", ClaimValue: "eng", RoleID: "engineering"}}}

	t.Run("onto a referenced name", func(t *testing.T) {
		api := newRBACAPI(t, withRBACYAML(referenced))
		expectStatus(t, api.rbacRename(api.createRole("incident_responder").ID, "engineering"), http.StatusConflict)
	})
	t.Run("away from a referenced name", func(t *testing.T) {
		api := newRBACAPI(t, withRBACYAML(referenced))
		expectStatus(t, api.rbacRename(api.createRole("engineering").ID, "engineering_v2"), http.StatusConflict)
	})
	t.Run("onto a configuration role's name, then elsewhere", func(t *testing.T) {
		api := newRBACAPI(t, withRBACYAML(rbac.YAMLState{
			Roles:        rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{"shadowed": {Scopes: []string{rbac.ScopeAgentBuilderRead}}}},
			DefaultRoles: []string{"shadowed"},
		}))
		role := api.createRole("shadowed_db")
		expectStatus(t, api.rbacRename(role.ID, "shadowed"), http.StatusConflict)
		expectStatus(t, api.rbacRename(role.ID, "shadowed_db_v2"), http.StatusOK)
	})
	t.Run("between names nothing references", func(t *testing.T) {
		api := newRBACAPI(t, withRBACYAML(rbac.YAMLState{ClaimMappings: []rbac.DBClaimMapping{{OIDCProvider: "azure", ClaimValue: "eng", RoleID: "other_role"}}}))
		expectStatus(t, api.rbacRename(api.createRole("incident_responder").ID, "incident_responder_v2"), http.StatusOK)
	})
}

func TestRoles_NameConflictMarksBothRowsOfASharedName(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	shared := &store.RBACRole{Name: "yaml_viewer", CreatedBy: "seed", UpdatedBy: "seed"}
	if err := store.NewRBACRepo(api.core.DB()).CreateRole(t.Context(), shared, []string{rbac.ScopeAgentBuilderRead}, nil); err != nil {
		t.Fatalf("seed role sharing a configuration name: %v", err)
	}
	api.createRole("incident_responder")

	conflicts := map[string]bool{}
	for _, r := range decodeList[Role](t, api.call(http.MethodGet, "/rbac/roles?pageSize=100", nil)).Data {
		conflicts[r.Name+"/"+r.Origin] = r.NameConflict
	}
	for row, want := range map[string]bool{
		"yaml_viewer/" + entplatform.RBACOriginYAML:          true,
		"yaml_viewer/" + entplatform.RBACOriginCustom:        true,
		"yaml_operator/" + entplatform.RBACOriginYAML:        false,
		"incident_responder/" + entplatform.RBACOriginCustom: false,
	} {
		if got, ok := conflicts[row]; !ok || got != want {
			t.Errorf("%s nameConflict = %v (listed %v), want %v", row, got, ok, want)
		}
	}
	if got := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer"), nil)); !got.NameConflict {
		t.Error("GET of the configuration row nameConflict = false, want true")
	}
	if got := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_operator"), nil)); got.NameConflict {
		t.Error("GET of an unshared configuration role nameConflict = true, want false")
	}
}

func TestRoles_BuiltinRoleIsListedAndAssignable(t *testing.T) {
	api := newRBACAPI(t)
	if got := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+builtinRoleID, nil)); got.Origin != entplatform.RBACOriginBuiltin {
		t.Errorf("GET origin %q, want %q", got.Origin, entplatform.RBACOriginBuiltin)
	}
	listed := decodeList[Role](t, api.call(http.MethodGet, "/rbac/roles?origin=builtin&pageSize=100", nil)).Data
	if !slices.ContainsFunc(listed, func(r Role) bool { return r.ID == builtinRoleID && r.Origin == entplatform.RBACOriginBuiltin }) {
		t.Errorf("origin=builtin = %v, want it to list %s", roleNames(listed), builtinRoleID)
	}
	if got := api.createGrant(emailSubject("dana@example.org"), builtinRoleID); got.RoleID != builtinRoleID {
		t.Errorf("grant roleId %q, want %q", got.RoleID, builtinRoleID)
	}
}

func TestGrants_ListSkipsLegacyGatewayKeys(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
		"alice@example.org": {Roles: []string{"yaml_operator"}},
		"gateway_specific_identities:slack:bob@example.org": {Roles: []string{"yaml_viewer"}},
	}}
	api := newRBACAPI(t, withRBACYAML(state))

	var sawAlice bool
	for _, g := range decodeList[Grant](t, api.call(http.MethodGet, "/rbac/grants?origin=yaml&pageSize=100", nil)).Data {
		if g.Subject == nil {
			continue
		}
		if strings.Contains(g.Subject.Value, "bob@example.org") {
			t.Errorf("legacy gateway-scoped key listed: %+v", g.Subject)
		}
		sawAlice = sawAlice || g.Subject.Value == "alice@example.org"
	}
	if !sawAlice {
		t.Error("the configuration grant for alice is missing")
	}
}

func TestClaimMappings_ReplaceRefusesAConfigurationRole(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	mapping := api.createClaimMapping("Engineering", "eng", api.createRole("editor").ID)
	rec := api.call(http.MethodPut, "/rbac/claimMappings/"+mapping.ID, map[string]any{
		"name": "Engineering", "claimValue": "eng", "roleIds": []string{entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")},
	})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "roleIds")
}

func TestClaimMappings_PlaceholderProviderIs422(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(claimMappingYAML()))
	rec := api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{
		"name": "Bad", "oidcProvider": rbac.ProviderSentinelEnterprise, "claimValue": "eng2", "roleIds": []string{api.createRole("responder").ID},
	})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "oidcProvider")
	if decodeError(t, rec).ErrorID == "" {
		t.Error("errorId is empty")
	}
}

func TestGrants_RoleTheResolverHasNotSeenIs503(t *testing.T) {
	api := newRBACAPI(t)
	unseen := &store.RBACRole{Name: "reader", CreatedBy: "seed", UpdatedBy: "seed"}
	if err := store.NewRBACRepo(api.core.DB()).CreateRole(t.Context(), unseen, []string{"agent:*:read"}, nil); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	rec := api.call(http.MethodPost, "/rbac/grants", map[string]any{"subject": emailSubject("grantee@example.com"), "roleId": unseen.ID})
	expectStatus(t, rec, http.StatusServiceUnavailable)
	if strings.Contains(rec.Body.String(), "composed state") {
		t.Errorf("503 body leaks the cause: %s", rec.Body.String())
	}
}
