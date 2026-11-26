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

func TestDefaultRoles_YAMLFallbackAndOverride(t *testing.T) {
	state := testRBACYAML
	state.DefaultRoles = []string{"yaml_viewer"}
	api := newRBACAPI(t, withRBACYAML(state))
	yamlViewer := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")

	rec := api.call(http.MethodGet, "/rbac/defaultRoles", nil)
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[DefaultRoles](t, rec)
	if got.Origin != entplatform.RBACOriginYAML || !slices.Equal(got.RoleIDs, []string{yamlViewer}) {
		t.Errorf("fallback = %+v, want yaml [%s]", got, yamlViewer)
	}

	role := api.createRole("everyone")
	rec = api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusOK)
	got = decodeData[DefaultRoles](t, rec)
	if got.Origin != entplatform.RBACOriginCustom || !slices.Equal(got.RoleIDs, []string{role.ID}) {
		t.Errorf("override = %+v, want custom [%s]", got, role.ID)
	}
	if again := decodeData[DefaultRoles](t, api.call(http.MethodGet, "/rbac/defaultRoles", nil)); !slices.Equal(again.RoleIDs, got.RoleIDs) || again.Origin != got.Origin {
		t.Errorf("GET after PUT = %+v, want %+v", again, got)
	}

	rec = api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{}})
	expectStatus(t, rec, http.StatusOK)
	got = decodeData[DefaultRoles](t, rec)
	if got.Origin != entplatform.RBACOriginYAML || !slices.Equal(got.RoleIDs, []string{yamlViewer}) {
		t.Errorf("cleared = %+v, want yaml [%s]", got, yamlViewer)
	}

	expectStatus(t, api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{role.ID}}), http.StatusOK)
	expectStatus(t, api.call(http.MethodPut, "/rbac/defaultRoles", `{}`), http.StatusOK)
	if got = decodeData[DefaultRoles](t, api.call(http.MethodGet, "/rbac/defaultRoles", nil)); got.Origin != entplatform.RBACOriginYAML {
		t.Errorf("a body without roleIds left origin %q, want yaml", got.Origin)
	}
}

func TestDefaultRoles_NoneIsAnEmptyList(t *testing.T) {
	api := newRBACAPI(t)
	raw := rawData(t, api.call(http.MethodGet, "/rbac/defaultRoles", nil))
	if ids, ok := raw["roleIds"].([]any); !ok || len(ids) != 0 {
		t.Errorf("roleIds = %v, want []", raw["roleIds"])
	}
}

func TestDefaultRoles_Validation(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	expectDetail(t, api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{missingID}}), http.StatusUnprocessableEntity, "roleIds")
	yamlRole := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")
	expectDetail(t, api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{yamlRole}}), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPut, "/rbac/defaultRoles", `{"roleIds": [], "colour": "red"}`), http.StatusBadRequest, "colour")
	expectStatus(t, api.call(http.MethodPut, "/rbac/defaultRoles", `{"roleIds": [`), http.StatusBadRequest)
}

func TestDefaultRoles_Scopes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("scoped")
	body := map[string]any{"roleIds": []string{role.ID}}

	expectStatus(t, api.call(http.MethodGet, "/rbac/defaultRoles", nil, rbac.ScopeRBACRead), http.StatusOK)
	expectMessage(t, api.call(http.MethodPut, "/rbac/defaultRoles", body, rbac.ScopeRBACRead), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodPut, "/rbac/defaultRoles", body, "rbac:*:*"), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodGet, "/rbac/defaultRoles", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPut, "/rbac/defaultRoles", body, "*:*:*"), http.StatusOK)
}

func TestDefaultRoles_MiniIDP(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	expectStatus(t, api.call(http.MethodGet, "/rbac/defaultRoles", nil), http.StatusOK)
	expectMessage(t, api.call(http.MethodPut, "/rbac/defaultRoles", map[string]any{"roleIds": []string{}}), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}
