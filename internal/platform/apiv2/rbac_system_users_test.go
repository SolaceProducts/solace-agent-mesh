// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const msgBuiltinSystemUser = "Built-in service account shipped and maintained by Solace; it cannot be modified, deleted, or granted roles through the API"

func (a *entrypointAPI) createSystemUser(name string, roleIDs ...string) ServiceAccount {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": name, "roleIds": roleIDs})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[ServiceAccount](a.t, rec)
	expectLocation(a.t, rec, "/rbac/serviceAccounts/"+created.ID)
	return created
}

func systemUserNames(users []ServiceAccount) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u.Name
	}
	return out
}

func TestSystemUsers_Lifecycle(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("su-role")
	other := api.createRole("su-other-role")

	rec := api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "webhook", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[ServiceAccount](t, rec)
	expectLocation(t, rec, "/rbac/serviceAccounts/"+created.ID)
	if created.Type != "serviceAccount" || created.Name != "webhook" || created.Sub != "system:webhook" || created.Origin != entplatform.RBACOriginCustom {
		t.Errorf("created = %+v", created)
	}
	if !slices.Equal(created.RoleIDs, []string{role.ID}) {
		t.Errorf("roleIds %v, want [%s]", created.RoleIDs, role.ID)
	}
	if created.CreatedBy == nil || *created.CreatedBy == "" {
		t.Error("createdBy is null on a system user created through the API")
	}
	raw := rawData(t, rec)
	if v, ok := raw["displayName"]; !ok || v != nil {
		t.Errorf("displayName not sent = %v (present %v), want null", v, ok)
	}
	for _, field := range []string{"createdTime", "updatedTime"} {
		if s, _ := raw[field].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", field, raw[field])
		}
	}

	rec = api.call(http.MethodGet, "/rbac/serviceAccounts/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[ServiceAccount](t, rec); got.ID != created.ID || got.Sub != "system:webhook" {
		t.Errorf("get = %+v", got)
	}

	rec = api.call(http.MethodPut, "/rbac/serviceAccounts/"+created.ID, map[string]any{"displayName": "Webhook", "roleIds": []string{other.ID}})
	expectStatus(t, rec, http.StatusOK)
	replaced := decodeData[ServiceAccount](t, rec)
	if replaced.DisplayName == nil || *replaced.DisplayName != "Webhook" || !slices.Equal(replaced.RoleIDs, []string{other.ID}) {
		t.Errorf("replaced = %+v", replaced)
	}
	if replaced.Name != "webhook" || replaced.UpdatedBy == nil {
		t.Errorf("replace lost identity or audit: %+v", replaced)
	}

	rec = api.call(http.MethodPut, "/rbac/serviceAccounts/"+created.ID, map[string]any{"roleIds": []string{other.ID}})
	expectStatus(t, rec, http.StatusOK)
	if v, ok := rawData(t, rec)["displayName"]; !ok || v != nil {
		t.Errorf("displayName left out of a replace = %v (present %v), want null", v, ok)
	}

	page := decodeList[ServiceAccount](t, api.call(http.MethodGet, "/rbac/serviceAccounts", nil))
	if !slices.Contains(systemUserNames(page.Data), "webhook") {
		t.Errorf("list %v lacks the system user", systemUserNames(page.Data))
	}

	expectStatus(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/rbac/serviceAccounts/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/"+created.ID, nil), http.StatusNotFound)
}

func TestSystemUsers_TakenNameIs409(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("su-role")
	api.createSystemUser("dup", role.ID)

	rec := api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "dup", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusConflict)

}

func TestSystemUsers_BuiltinsAre409(t *testing.T) {
	api := newRBACAPI(t)
	page := decodeList[ServiceAccount](t, api.call(http.MethodGet, "/rbac/serviceAccounts?pageSize=100", nil))
	var builtin *ServiceAccount
	for i := range page.Data {
		if page.Data[i].Sub == rbac.DefaultSystemUserSub {
			builtin = &page.Data[i]
		}
	}
	if builtin == nil {
		t.Fatalf("list %v lacks %s", systemUserNames(page.Data), rbac.DefaultSystemUserSub)
	}
	if builtin.Origin != entplatform.RBACOriginBuiltin || len(builtin.RoleIDs) == 0 {
		t.Errorf("builtin = %+v, want origin builtin with roles", builtin)
	}
	for _, id := range builtin.RoleIDs {
		if _, err := uuid.Parse(id); err != nil {
			t.Errorf("roleId %q of a built-in system user is not a UUID", id)
		}
	}

	rec := api.call(http.MethodGet, "/rbac/serviceAccounts/"+builtin.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	role := api.createRole("su-role")
	body := map[string]any{"roleIds": []string{role.ID}}
	expectMessage(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+builtin.ID, body), http.StatusConflict, msgBuiltinSystemUser)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/"+builtin.ID, nil), http.StatusConflict, msgBuiltinSystemUser)
	expectMessage(t, api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "default", "roleIds": []string{role.ID}}),
		http.StatusConflict, msgBuiltinSystemUser)
}

func TestSystemUsers_Paging(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("su-role")
	for _, name := range []string{"su-a", "su-b", "su-c"} {
		api.createSystemUser(name, role.ID)
	}
	names := func(query string) ([]string, int) {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/serviceAccounts?"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		page := decodeList[ServiceAccount](t, rec)
		return systemUserNames(page.Data), page.Meta.Pagination.Count
	}

	if got, _ := names("search=su-&pageSize=2"); !slices.Equal(got, []string{"su-a", "su-b"}) {
		t.Errorf("page 1 = %v, want [su-a su-b]", got)
	}
	if got, _ := names("search=su-&pageSize=2&pageNumber=2"); !slices.Equal(got, []string{"su-c"}) {
		t.Errorf("page 2 = %v, want [su-c]", got)
	}
	if got, _ := names("search=su-&pageSize=2&pageNumber=3"); len(got) != 0 {
		t.Errorf("page past the end = %v, want []", got)
	}
	if got, total := names("search=SU-B"); !slices.Equal(got, []string{"su-b"}) || total != 1 {
		t.Errorf("search = %v of %d, want [su-b] of 1", got, total)
	}
	if got, _ := names("search=su-&sort=name:desc"); !slices.Equal(got, []string{"su-c", "su-b", "su-a"}) {
		t.Errorf("name desc = %v", got)
	}
	expectDetail(t, api.call(http.MethodGet, "/rbac/serviceAccounts?sort=bogus:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/rbac/serviceAccounts?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
}

func TestSystemUsers_Validation(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("su-role")
	su := api.createSystemUser("valid", role.ID)

	expectDetail(t, api.call(http.MethodGet, "/rbac/serviceAccounts/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPut, "/rbac/serviceAccounts/not-a-uuid", map[string]any{"roleIds": []string{role.ID}}), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "x"}), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"roleIds": []string{role.ID}}), http.StatusUnprocessableEntity, "name")
	expectDetail(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+su.ID, map[string]any{}), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "x", "roleIds": []string{missingID}}), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+su.ID, map[string]any{"roleIds": []string{missingID}}), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPost, "/rbac/serviceAccounts", `{"name":"x","roleIds":["`+role.ID+`"],"colour":"red"}`), http.StatusBadRequest, "colour")
	expectDetail(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+su.ID, `{"name":"renamed","roleIds":["`+role.ID+`"]}`), http.StatusBadRequest, "name")

	human := &store.User{Issuer: "https://idp.example.com", Sub: "human-1", ProvisionedVia: "jit", CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewUsersRepo(api.core.DB()).UpsertUser(t.Context(), human, nil); err != nil {
		t.Fatalf("seed human user: %v", err)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		expectStatus(t, api.call(method, "/rbac/serviceAccounts/"+human.ID, nil), http.StatusNotFound)
	}
	expectStatus(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+human.ID, map[string]any{"roleIds": []string{role.ID}}), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/rbac/serviceAccounts/"+missingID, nil), http.StatusNotFound)
}

func TestSystemUsers_Scopes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("su-role")
	su := api.createSystemUser("scoped", role.ID)
	reader := rbac.ScopeRBACRead
	body := map[string]any{"name": "x", "roleIds": []string{role.ID}}

	expectStatus(t, api.call(http.MethodGet, "/rbac/serviceAccounts", nil, reader), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/rbac/serviceAccounts/"+su.ID, nil, reader), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/serviceAccounts", body, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+su.ID, body, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/"+su.ID, nil, "rbac:*:*"), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodGet, "/rbac/serviceAccounts", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPost, "/rbac/serviceAccounts", body, "*:*:*"), http.StatusCreated)
}

func TestSystemUsers_MiniIDPRefusesWrites(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	body := map[string]any{"name": "x", "roleIds": []string{builtinRoleID}}
	expectStatus(t, api.call(http.MethodGet, "/rbac/serviceAccounts", nil), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/serviceAccounts", body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodPut, "/rbac/serviceAccounts/"+missingID, map[string]any{"roleIds": body["roleIds"]}), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/"+missingID, nil), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}

func TestSystemUsers_ConcurrentCreatesOfOneNameYieldOne(t *testing.T) {
	api := newRBACAPI(t)
	const racers = 6
	roles := make([]string, racers)
	for i := range roles {
		roles[i] = api.createRole(fmt.Sprintf("race-role-%d", i)).ID
	}
	codes := make([]int, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			codes[i] = api.call(http.MethodPost, "/rbac/serviceAccounts", map[string]any{"name": "racer", "roleIds": []string{roles[i]}}).Code
		})
	}
	wg.Wait()
	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	if created != 1 || conflicts != racers-1 {
		t.Fatalf("statuses %v, want one 201 and %d 409", codes, racers-1)
	}
	users := decodeList[ServiceAccount](t, api.call(http.MethodGet, "/rbac/serviceAccounts?search=racer", nil)).Data
	if len(users) != 1 || len(users[0].RoleIDs) != 1 {
		t.Errorf("racer = %+v, want one system user holding only the winner's role", users)
	}
}

func TestSystemUsers_DeprecatedRoutesAliasServiceAccounts(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("su-role")

	rec := api.call(http.MethodPost, "/rbac/systemUsers", map[string]any{"name": "legacy-webhook", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[SystemUser](t, rec)
	if created.Type != "systemUser" || created.Sub != "system:legacy-webhook" {
		t.Errorf("created on the deprecated route = %+v, want type systemUser", created)
	}
	const noticeURL = "https://docs.solace.com/Agent-Mesh/Framework/reference/deprecations.htm#system-users-renamed-to-service-accounts"
	for header, want := range map[string]string{
		"X-Solace-API-Deprecated": noticeURL,
		"Deprecation":             "@1795478400",
		"Link":                    "<" + noticeURL + `>; rel="deprecation"`,
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s on the deprecated route = %q, want %q", header, got, want)
		}
	}

	rec = api.call(http.MethodGet, "/rbac/serviceAccounts/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[ServiceAccount](t, rec); got.Type != "serviceAccount" || got.Name != "legacy-webhook" {
		t.Errorf("the same record on the new route = %+v, want type serviceAccount", got)
	}
	if got := rec.Header().Get("X-Solace-API-Deprecated"); got != "" {
		t.Errorf("the new route is marked deprecated: %q", got)
	}

	rec = api.call(http.MethodPut, "/rbac/systemUsers/"+created.ID, map[string]any{"displayName": "Legacy", "roleIds": []string{role.ID}})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[SystemUser](t, rec); got.Type != "systemUser" || got.DisplayName == nil || *got.DisplayName != "Legacy" {
		t.Errorf("replaced on the deprecated route = %+v", got)
	}

	page := decodeList[SystemUser](t, api.call(http.MethodGet, "/rbac/systemUsers?pageSize=100", nil))
	if !slices.ContainsFunc(page.Data, func(u SystemUser) bool { return u.ID == created.ID && u.Type == "systemUser" }) {
		t.Errorf("deprecated list lacks %s with type systemUser", created.ID)
	}

	expectStatus(t, api.call(http.MethodDelete, "/rbac/serviceAccounts/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/rbac/systemUsers/"+created.ID, nil), http.StatusNotFound)
}

func TestSystemUsers_HandWrittenRemovalDatesMatchTheRoutes(t *testing.T) {
	docs := map[string]string{
		"SystemUser":                        SystemUser{}.APIDoc().Type,
		"CreateSystemUserRequest":           CreateSystemUserRequest{}.APIDoc().Type,
		"ReplaceSystemUserRequest":          ReplaceSystemUserRequest{}.APIDoc().Type,
		"UserAccess.isSystemUser":           UserAccess{}.APIDoc().Field("IsSystemUser").Doc,
		"BudgetAssignmentUser.isSystemUser": BudgetAssignmentUser{}.APIDoc().Field("IsSystemUser").Doc,
		"BudgetSubjectUser.isSystemUser":    BudgetSubjectUser{}.APIDoc().Field("IsSystemUser").Doc,
		"UsageUser.isSystemUser":            UsageUser{}.APIDoc().Field("IsSystemUser").Doc,
	}
	dates := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	for name, doc := range docs {
		if got := dates.FindAllString(doc, -1); len(got) != 1 || got[0] != systemUserRoutesRemoval {
			t.Errorf("%s names dates %v; want only the routes' removal date %s", name, got, systemUserRoutesRemoval)
		}
	}
}
