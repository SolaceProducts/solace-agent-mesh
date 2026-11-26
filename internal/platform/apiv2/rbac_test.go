// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	builtinRoleID   = "00000000-0000-0000-0000-000000000001"
	msgNotSuperuser = "RBAC management requires a super administrator"
)

var testRBACYAML = rbac.YAMLState{
	Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
		"yaml_viewer":   {Description: "Reads agents.", Scopes: []string{rbac.ScopeAgentBuilderRead}},
		"yaml_operator": {Description: "Runs agents.", Scopes: []string{"agent:*:invoke"}, Inherits: []string{"yaml_viewer"}},
	}},
}

func newRBACAPI(t *testing.T, configure ...func(*entplatform.RouteDeps)) *entrypointAPI {
	t.Helper()
	core, _ := newTestCoreWithConfig(t, nil, configure...)
	return &entrypointAPI{apiClient: newAPIClient(t, core)}
}

func withRBACYAML(state rbac.YAMLState) func(*entplatform.RouteDeps) {
	return func(d *entplatform.RouteDeps) { d.RBACYAMLState = state }
}

func withMiniIDP(d *entplatform.RouteDeps) { d.MiniIDPMode = true }

func expectMessage(t *testing.T, rec *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	expectStatus(t, rec, status)
	if body := decodeError(t, rec); body.Message != message {
		t.Errorf("message %q, want %q", body.Message, message)
	}
}

func TestRBAC_YAMLReferencesToDatabaseRolesUseTheirUUID(t *testing.T) {
	const builtinName = "sam_manager"
	roles := map[string]rbac.RoleDefinition{}
	maps.Copy(roles, testRBACYAML.Roles.Roles)
	roles["yaml_lead"] = rbac.RoleDefinition{Description: "Leads.", Inherits: []string{builtinName}}
	state := rbac.YAMLState{
		Roles:             rbac.RolesConfig{Roles: roles},
		Users:             rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"bob@example.com": {Roles: []string{builtinName}}}},
		DefaultRoles:      []string{builtinName},
		ClaimMappings:     []rbac.DBClaimMapping{{OIDCProvider: "okta", ClaimKey: "groups", ClaimValue: "managers", RoleID: builtinName}},
		ClaimOIDCProvider: "okta",
		ClaimKey:          "groups",
	}
	api := newRBACAPI(t, withRBACYAML(state))

	grants := decodeList[Grant](t, api.call(http.MethodGet, "/rbac/grants?origin=yaml&roleId="+builtinRoleID, nil)).Data
	if len(grants) != 1 || grants[0].RoleID != builtinRoleID || grants[0].Subject == nil || grants[0].Subject.Value != "bob@example.com" {
		t.Errorf("YAML grants of %s = %+v, want bob's grant naming it by UUID", builtinName, grants)
	}
	if got := decodeData[DefaultRoles](t, api.call(http.MethodGet, "/rbac/defaultRoles", nil)); !slices.Equal(got.RoleIDs, []string{builtinRoleID}) {
		t.Errorf("default roleIds = %v, want [%s]", got.RoleIDs, builtinRoleID)
	}
	mappings := decodeList[ClaimMapping](t, api.call(http.MethodGet, "/rbac/claimMappings?origin=yaml&roleId="+builtinRoleID, nil)).Data
	if len(mappings) != 1 || !slices.Equal(mappings[0].RoleIDs, []string{builtinRoleID}) {
		t.Errorf("YAML claim mappings of %s = %+v, want one naming it by UUID", builtinName, mappings)
	}
	lead := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_lead"), nil))
	if !slices.Equal(lead.Inherits, []string{builtinRoleID}) {
		t.Errorf("yaml_lead inherits %v, want [%s]", lead.Inherits, builtinRoleID)
	}
	expectStatus(t, api.call(http.MethodGet, "/rbac/roles/"+builtinRoleID, nil), http.StatusOK)

	viewer := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")
	if got := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_operator"), nil)); !slices.Equal(got.Inherits, []string{viewer}) {
		t.Errorf("a name YAML defines still resolves to the YAML role: inherits %v, want [%s]", got.Inherits, viewer)
	}
}

func TestRBAC_UUIDShapedYAMLReferencesAreNames(t *testing.T) {
	const uuidNamedRole = "11111111-2222-3333-4444-555555555555"
	roles := map[string]rbac.RoleDefinition{}
	maps.Copy(roles, testRBACYAML.Roles.Roles)
	roles["yaml_lead"] = rbac.RoleDefinition{Description: "Leads.", Inherits: []string{builtinRoleID}}
	roles[uuidNamedRole] = rbac.RoleDefinition{Description: "Named like a UUID."}
	state := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: roles},
		Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"bob@example.com":         {Roles: []string{builtinRoleID}},
			rbac.DefaultSystemUserSub: {Roles: []string{uuidNamedRole}},
		}},
		DefaultRoles:      []string{builtinRoleID},
		ClaimMappings:     []rbac.DBClaimMapping{{OIDCProvider: "okta", ClaimKey: "groups", ClaimValue: "managers", RoleID: builtinRoleID}},
		ClaimOIDCProvider: "okta",
		ClaimKey:          "groups",
	}
	api := newRBACAPI(t, withRBACYAML(state))
	nameOnly := entplatform.YAMLRowID(entplatform.YAMLKindRole, builtinRoleID)

	if grants := decodeList[Grant](t, api.call(http.MethodGet, "/rbac/grants?origin=yaml&roleId="+builtinRoleID, nil)).Data; len(grants) != 0 {
		t.Errorf("YAML grants of the database role = %+v, want none", grants)
	}
	grants := decodeList[Grant](t, api.call(http.MethodGet, "/rbac/grants?origin=yaml&roleId="+nameOnly, nil)).Data
	if len(grants) != 1 || grants[0].Subject == nil || grants[0].Subject.Value != "bob@example.com" || grants[0].RoleID != nameOnly {
		t.Errorf("YAML grants of the name = %+v, want bob's with roleId %s", grants, nameOnly)
	}
	if got := decodeData[DefaultRoles](t, api.call(http.MethodGet, "/rbac/defaultRoles", nil)); !slices.Equal(got.RoleIDs, []string{nameOnly}) {
		t.Errorf("default roleIds = %v, want [%s]", got.RoleIDs, nameOnly)
	}
	if mappings := decodeList[ClaimMapping](t, api.call(http.MethodGet, "/rbac/claimMappings?origin=yaml&roleId="+builtinRoleID, nil)).Data; len(mappings) != 0 {
		t.Errorf("YAML claim mappings of the database role = %+v, want none", mappings)
	}
	if mappings := decodeList[ClaimMapping](t, api.call(http.MethodGet, "/rbac/claimMappings?origin=yaml", nil)).Data; len(mappings) != 1 || !slices.Equal(mappings[0].RoleIDs, []string{nameOnly}) {
		t.Errorf("YAML claim mappings = %+v, want one with roleIds [%s]", mappings, nameOnly)
	}
	lead := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_lead"), nil))
	if !slices.Equal(lead.Inherits, []string{nameOnly}) {
		t.Errorf("yaml_lead inherits %v, want [%s]", lead.Inherits, nameOnly)
	}

	uuidNamedID := entplatform.YAMLRowID(entplatform.YAMLKindRole, uuidNamedRole)
	if got := decodeData[Role](t, api.call(http.MethodGet, "/rbac/roles/"+uuidNamedID, nil)); got.ID != uuidNamedID || got.Name != uuidNamedRole {
		t.Errorf("UUID-named YAML role = id %s name %s, want id %s", got.ID, got.Name, uuidNamedID)
	}
	defaultUser := decodeData[SystemUser](t, api.call(http.MethodGet, "/rbac/systemUsers/"+rbac.DefaultSystemUserID, nil))
	if len(defaultUser.RoleIDs) == 0 || defaultUser.RoleIDs[0] != uuidNamedID {
		t.Errorf("built-in service account roleIds %v, want the users file's UUID-named role first as %s", defaultUser.RoleIDs, uuidNamedID)
	}
}

func TestRBAC_DuplicatesSayWhy(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("taken-role")
	expectMessage(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "taken-role"}), http.StatusConflict,
		"A role with this name already exists")

	api.createSystemUser("taken-bot", role.ID)
	expectMessage(t, api.call(http.MethodPost, "/rbac/systemUsers", map[string]any{"name": "taken-bot", "roleIds": []string{role.ID}}), http.StatusConflict,
		"A service account with this name already exists")

	grant := map[string]any{"subject": emailSubject("taken@example.com"), "roleId": role.ID}
	expectStatus(t, api.call(http.MethodPost, "/rbac/grants", grant), http.StatusCreated)
	expectMessage(t, api.call(http.MethodPost, "/rbac/grants", grant), http.StatusConflict,
		"This role is already granted to that identity")

	mapping := map[string]any{"name": "first", "claimValue": "taken-group", "roleIds": []string{role.ID}}
	expectStatus(t, api.call(http.MethodPost, "/rbac/claimMappings", mapping), http.StatusCreated)
	mapping["name"] = "second"
	expectMessage(t, api.call(http.MethodPost, "/rbac/claimMappings", mapping), http.StatusConflict,
		"A claim mapping for this claim value already exists")
}

func TestRBAC_ClaimMappingWithNoProviderSendsNull(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("unconfigured")
	rec := api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{"name": "m", "claimValue": "v", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusCreated)
	raw := rawData(t, rec)
	for _, field := range []string{"oidcProvider", "claimKey"} {
		if v, ok := raw[field]; !ok || v != nil {
			t.Errorf("%s = %#v (present %v), want null", field, v, ok)
		}
	}
}

func TestRBAC_ReplaceWithNoProviderConfiguredClearsTheProvider(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("unconfigured-replace")
	rec := api.call(http.MethodPost, "/rbac/claimMappings", map[string]any{"name": "m", "oidcProvider": "auth0", "claimValue": "v", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[ClaimMapping](t, rec)

	rec = api.call(http.MethodPut, "/rbac/claimMappings/"+created.ID, map[string]any{"name": "m", "claimValue": "v", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusOK)
	if v, ok := rawData(t, rec)["oidcProvider"]; !ok || v != nil {
		t.Errorf("oidcProvider = %#v (present %v), want null", v, ok)
	}
}

func TestRBAC_ConfigurationClashesSayWhy(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	const clash = `A system role named "yaml_viewer" already exists; pick a different name`
	expectMessage(t, api.call(http.MethodPost, "/rbac/roles", map[string]any{"name": "yaml_viewer"}), http.StatusConflict, clash)

	role := api.createRole("renamable")
	expectMessage(t, api.call(http.MethodPut, "/rbac/roles/"+role.ID, map[string]any{"name": "yaml_viewer"}), http.StatusConflict, clash)

}
