// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func emailSubject(email string) map[string]any {
	return map[string]any{"type": "email", "value": email}
}

func (a *entrypointAPI) createGrant(subject map[string]any, roleID string) Grant {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/rbac/grants", map[string]any{"subject": subject, "roleId": roleID})
	expectStatus(a.t, rec, http.StatusCreated)
	created := decodeData[Grant](a.t, rec)
	expectLocation(a.t, rec, "/rbac/grants/"+created.ID)
	return created
}

func systemUserSubject(su ServiceAccount) map[string]any {
	return map[string]any{"type": "sub", "value": su.Sub, "issuer": "sam:system"}
}

func grantIDs(grants []Grant) []string {
	out := make([]string, len(grants))
	for i, g := range grants {
		out[i] = g.ID
	}
	return out
}

func TestGrants_Lifecycle(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("grantable")
	created := api.createGrant(emailSubject("pending@example.com"), role.ID)
	expectStatus(t, api.call(http.MethodPost, "/rbac/grants", map[string]any{"subject": emailSubject("PENDING@example.com"), "roleId": role.ID}), http.StatusConflict)

	if created.Type != "grant" || created.Origin != entplatform.RBACOriginCustom || created.RoleID != role.ID {
		t.Errorf("type %q origin %q role %q", created.Type, created.Origin, created.RoleID)
	}
	if created.State == nil || *created.State != entplatform.GrantStatePending {
		t.Errorf("state %v, want pending", created.State)
	}
	if created.Subject == nil || created.Subject.Type != "email" || created.Subject.Value != "pending@example.com" || created.Subject.Issuer != nil {
		t.Errorf("subject %+v", created.Subject)
	}
	if created.CreatedBy == nil || *created.CreatedBy == "" {
		t.Error("createdBy is null on a grant created through the API")
	}

	raw := rawData(t, api.call(http.MethodGet, "/rbac/grants/"+created.ID, nil))
	if v, ok := raw["userId"]; !ok || v != nil {
		t.Errorf("userId of a pending grant = %v (present %v), want null", v, ok)
	}
	for _, field := range []string{"createdTime", "updatedTime"} {
		if s, _ := raw[field].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", field, raw[field])
		}
	}
	if raw["id"] != created.ID || raw["type"] != "grant" {
		t.Errorf("GET id %v type %v", raw["id"], raw["type"])
	}

	page := decodeList[Grant](t, api.call(http.MethodGet, "/rbac/grants?origin=custom", nil))
	if !slices.Contains(grantIDs(page.Data), created.ID) {
		t.Errorf("list %v lacks the new grant", grantIDs(page.Data))
	}

	expectStatus(t, api.call(http.MethodDelete, "/rbac/grants/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants/"+created.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/rbac/grants/"+created.ID, nil), http.StatusNotFound)
}

func TestGrants_ConcreteAndDuplicateIs409(t *testing.T) {
	api := newRBACAPI(t)
	first, second := api.createRole("first"), api.createRole("second")
	subject := systemUserSubject(api.createSystemUser("grant-bot", first.ID))

	created := api.createGrant(subject, second.ID)
	if created.State == nil || *created.State != entplatform.GrantStateConcrete {
		t.Errorf("state %v, want concrete", created.State)
	}
	if created.UserID == nil || *created.UserID == "" {
		t.Error("a concrete grant has no userId")
	}
	if created.Subject == nil || created.Subject.Value != "system:grant-bot" {
		t.Errorf("subject %+v", created.Subject)
	}

	expectStatus(t, api.call(http.MethodPost, "/rbac/grants", map[string]any{"subject": subject, "roleId": second.ID}), http.StatusConflict)
	uppercaseStatus := http.StatusUnprocessableEntity
	if api.core.DB().Dialect() == "postgres" {
		uppercaseStatus = http.StatusConflict
	}
	expectStatus(t, api.call(http.MethodPost, "/rbac/grants", map[string]any{"subject": subject, "roleId": strings.ToUpper(second.ID)}), uppercaseStatus)

	got := decodeList[Grant](t, api.call(http.MethodGet, "/rbac/grants?state=concrete&roleId="+second.ID, nil))
	if !slices.Equal(grantIDs(got.Data), []string{created.ID}) {
		t.Errorf("concrete grants of the role = %v, want [%s]", grantIDs(got.Data), created.ID)
	}
}

func TestGrants_ConfiguredAndReservedSubjectsAre409(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"carol@example.com": {Roles: []string{"configured-role"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	role := api.createRole("configured-role")

	expectMessage(t, api.call(http.MethodPost, "/rbac/grants", map[string]any{
		"subject": emailSubject("carol@example.com"), "roleId": role.ID,
	}), http.StatusConflict, `Grant (identity="carol@example.com", role="configured-role") is defined in your configuration`)

	for _, reserved := range rbac.ReservedSystemUsers() {
		expectMessage(t, api.call(http.MethodPost, "/rbac/grants", map[string]any{
			"subject": map[string]any{"type": "sub", "value": reserved.Sub, "issuer": "sam:system"}, "roleId": role.ID,
		}), http.StatusConflict, msgBuiltinSystemUser)
	}
}

func TestGrants_ConcurrentCreatesOfOneGrantYieldOne(t *testing.T) {
	api := newRBACAPI(t)
	first, second := api.createRole("first"), api.createRole("second")
	subject := systemUserSubject(api.createSystemUser("race-bot", first.ID))
	const racers = 6
	codes := make([]int, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			codes[i] = api.call(http.MethodPost, "/rbac/grants", map[string]any{"subject": subject, "roleId": second.ID}).Code
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
}

func TestGrants_YAMLGrantsAreAddressableAndReadOnly(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"alice@example.com": {Roles: []string{"yaml_viewer"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	grantID := entplatform.YAMLRowID(entplatform.YAMLKindGrant, "alice@example.com|yaml_viewer")
	roleID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")

	rec := api.call(http.MethodGet, "/rbac/grants?origin=yaml", nil)
	expectStatus(t, rec, http.StatusOK)
	page := decodeList[Grant](t, rec)
	if !slices.Equal(grantIDs(page.Data), []string{grantID}) {
		t.Fatalf("yaml grants %v, want [%s]", grantIDs(page.Data), grantID)
	}
	if g := page.Data[0]; g.RoleID != roleID || g.Origin != entplatform.RBACOriginYAML || g.Subject == nil || g.Subject.Value != "alice@example.com" {
		t.Errorf("yaml grant %+v", g)
	}

	rec = api.call(http.MethodGet, "/rbac/grants/"+grantID, nil)
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	if raw["id"] != grantID || raw["roleId"] != roleID {
		t.Errorf("GET id %v roleId %v", raw["id"], raw["roleId"])
	}
	for _, field := range []string{"state", "userId", "createdBy", "updatedBy", "createdTime", "updatedTime"} {
		if v, ok := raw[field]; !ok || v != nil {
			t.Errorf("%s on a YAML grant = %v (present %v), want null", field, v, ok)
		}
	}

	expectMessage(t, api.call(http.MethodDelete, "/rbac/grants/"+grantID, nil), http.StatusConflict, msgYAMLGrantImmutable)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants/"+grantID, nil), http.StatusOK)
}

func TestGrants_Paging(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"alice@example.com": {Roles: []string{"yaml_viewer"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	roleA, roleB := api.createRole("role-a"), api.createRole("role-b")
	subject := systemUserSubject(api.createSystemUser("page-bot", roleA.ID))
	g1 := api.createGrant(emailSubject("one@example.com"), roleA.ID)
	g2 := api.createGrant(emailSubject("two@example.com"), roleB.ID)
	g3 := api.createGrant(subject, roleB.ID)

	ids := func(query string) []string {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/grants?"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		return grantIDs(decodeList[Grant](t, rec).Data)
	}

	if got := ids("state=pending&origin=custom"); len(got) != 2 || !slices.Contains(got, g1.ID) || !slices.Contains(got, g2.ID) {
		t.Errorf("pending = %v, want %s and %s", got, g1.ID, g2.ID)
	}
	if got := ids("state=concrete&roleId=" + roleB.ID); !slices.Equal(got, []string{g3.ID}) {
		t.Errorf("concrete of role-b = %v, want [%s]", got, g3.ID)
	}
	if got := ids("origin=yaml"); len(got) != 1 {
		t.Errorf("yaml = %v, want one", got)
	}
	if got := ids("state=pending"); slices.ContainsFunc(got, func(id string) bool {
		return id == entplatform.YAMLRowID(entplatform.YAMLKindGrant, "alice@example.com|yaml_viewer")
	}) {
		t.Errorf("a YAML grant has no state but is listed under state=pending: %v", got)
	}
	if got := ids("roleId=" + roleA.ID + "&origin=custom"); len(got) != 2 {
		t.Errorf("role-a grants = %v, want two", got)
	}

	if got := ids("origin=custom&state=pending&sort=roleId:asc"); !slices.Equal(got, []string{g1.ID, g2.ID}) {
		t.Errorf("roleId asc = %v, want [%s %s]", got, g1.ID, g2.ID)
	}
	if got := ids("origin=custom&state=pending&sort=roleId:desc"); !slices.Equal(got, []string{g2.ID, g1.ID}) {
		t.Errorf("roleId desc = %v, want [%s %s]", got, g2.ID, g1.ID)
	}
	if got := ids("origin=custom&sort=createdTime:desc"); got[0] != g3.ID {
		t.Errorf("createdTime desc starts with %s, want the newest %s", got[0], g3.ID)
	}

	if got := ids("origin=custom&pageSize=2"); len(got) != 2 {
		t.Errorf("page 1 = %v, want two", got)
	}
	if got := ids("origin=custom&pageSize=2&pageNumber=2"); len(got) != 2 {
		t.Errorf("page 2 = %v, want the two grants left", got)
	}
	if got := ids("origin=custom&pageSize=2&pageNumber=5"); len(got) != 0 {
		t.Errorf("page past the end = %v, want []", got)
	}

	all := ids("pageSize=100")
	if yamlGrant := entplatform.YAMLRowID(entplatform.YAMLKindGrant, "alice@example.com|yaml_viewer"); len(all) < 4 || all[0] != yamlGrant {
		t.Fatalf("all grants = %v, want the YAML grant first and at least three more", all)
	}
	if got := ids("pageSize=2&pageNumber=2"); !slices.Equal(got, all[2:4]) {
		t.Errorf("all page 2 = %v, want %v: the database rows from offset 1, past the YAML grant", got, all[2:4])
	}

	expectDetail(t, api.call(http.MethodGet, "/rbac/grants?state=bogus", nil), http.StatusUnprocessableEntity, "state")
	expectDetail(t, api.call(http.MethodGet, "/rbac/grants?origin=builtin", nil), http.StatusUnprocessableEntity, "origin")
	expectDetail(t, api.call(http.MethodGet, "/rbac/grants?roleId=not-a-uuid", nil), http.StatusBadRequest, "roleId")
	expectDetail(t, api.call(http.MethodGet, "/rbac/grants?sort=userId:asc", nil), http.StatusUnprocessableEntity, "sort")
}

func TestGrants_ExpandUser(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"alice@example.com": {Roles: []string{"yaml_viewer"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	role := api.createRole("expanded")
	api.seedUser(userSeed{sub: "alice-sub", email: "alice@example.com"})
	api.seedUser(userSeed{sub: "twin-a", email: "twin@example.com"})
	api.seedUser(userSeed{sub: "twin-b", email: "twin@example.com"})
	alice := api.createGrant(emailSubject("alice@example.com"), role.ID)
	twin := api.createGrant(subSubject("twin-a"), role.ID)
	bot := api.createSystemUser("expand-bot", role.ID)
	pending := api.createGrant(emailSubject("nobody@example.com"), role.ID)
	yamlGrant := entplatform.YAMLRowID(entplatform.YAMLKindGrant, "alice@example.com|yaml_viewer")

	byID := func(query string) map[string]Grant {
		t.Helper()
		rec := api.call(http.MethodGet, "/rbac/grants?pageSize=100"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		out := map[string]Grant{}
		for _, g := range decodeList[Grant](t, rec).Data {
			out[g.ID] = g
		}
		return out
	}

	expanded := byID("&expand=user")
	if u := expanded[alice.ID].User; u == nil || u.Email == nil || *u.Email != "alice@example.com" || u.EmailShared || u.Sub != "alice-sub" || u.Issuer != "https://idp.example.com" {
		t.Errorf("alice's user = %+v", u)
	}
	if u := expanded[twin.ID].User; u == nil || !u.EmailShared || u.Sub != "twin-a" {
		t.Errorf("twin's user = %+v, want twin-a with a shared email", u)
	}
	var botGrant *Grant
	for _, g := range expanded {
		if g.Subject != nil && g.Subject.Value == bot.Sub {
			botGrant = &g
		}
	}
	if botGrant == nil || botGrant.User == nil || botGrant.User.Email != nil || botGrant.User.Sub != bot.Sub || botGrant.User.Issuer != "sam:system" {
		t.Errorf("service account's grant = %+v, want a user with no email", botGrant)
	}
	for _, id := range []string{pending.ID, yamlGrant} {
		if g, ok := expanded[id]; !ok || g.User != nil {
			t.Errorf("grant %s user = %+v (listed %t), want null", id, g.User, ok)
		}
	}

	rec := api.call(http.MethodGet, "/rbac/grants?pageSize=100", nil)
	expectStatus(t, rec, http.StatusOK)
	for _, g := range decodeList[map[string]any](t, rec).Data {
		if v, ok := g["user"]; !ok || v != nil {
			t.Errorf("grant %v without expand has user %v (present %t), want null", g["id"], v, ok)
		}
	}
	if u := decodeData[Grant](t, api.call(http.MethodGet, "/rbac/grants/"+alice.ID, nil)).User; u != nil {
		t.Errorf("GET of one grant has user %+v, want null", u)
	}

	expectDetail(t, api.call(http.MethodGet, "/rbac/grants?expand=bogus", nil), http.StatusUnprocessableEntity, "expand")
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants?expand=user", nil, rbac.ScopeRBACRead), http.StatusOK)
}

func TestGrants_Validation(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	role := api.createRole("validated")
	post := func(body any) *httptest.ResponseRecorder { return api.call(http.MethodPost, "/rbac/grants", body) }

	expectDetail(t, api.call(http.MethodGet, "/rbac/grants/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodDelete, "/rbac/grants/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants/"+missingID, nil), http.StatusNotFound)

	expectDetail(t, post(map[string]any{"roleId": role.ID}), http.StatusUnprocessableEntity, "subject")
	expectDetail(t, post(map[string]any{"subject": map[string]any{"type": "bogus", "value": "x"}, "roleId": role.ID}), http.StatusUnprocessableEntity, "type")
	expectDetail(t, post(map[string]any{"subject": map[string]any{"type": "email"}, "roleId": role.ID}), http.StatusUnprocessableEntity, "value")
	expectDetail(t, post(map[string]any{"subject": emailSubject("a@example.com")}), http.StatusUnprocessableEntity, "roleId")
	expectDetail(t, post(`{"subject":{"type":"email","value":"a@example.com"},"roleId":"`+role.ID+`","colour":"red"}`), http.StatusBadRequest, "colour")

	yamlRole := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")
	expectDetail(t, post(map[string]any{"subject": emailSubject("a@example.com"), "roleId": yamlRole}), http.StatusUnprocessableEntity, "roleId")
	expectDetail(t, post(map[string]any{"subject": emailSubject("a@example.com"), "roleId": missingID}), http.StatusUnprocessableEntity, "roleId")
}

func TestGrants_Scopes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("scoped")
	grant := api.createGrant(emailSubject("scoped@example.com"), role.ID)
	reader := rbac.ScopeRBACRead
	body := map[string]any{"subject": emailSubject("other@example.com"), "roleId": role.ID}

	expectStatus(t, api.call(http.MethodGet, "/rbac/grants", nil, reader), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants/"+grant.ID, nil, reader), http.StatusOK)
	expectMessage(t, api.call(http.MethodPost, "/rbac/grants", body, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/grants/"+grant.ID, nil, reader), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants/"+grant.ID, nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants", nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants/"+grant.ID, nil), http.StatusOK)
}

func TestGrants_MiniIDPRefusesWrites(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	expectStatus(t, api.call(http.MethodGet, "/rbac/grants", nil), http.StatusOK)
	body := map[string]any{"subject": emailSubject("a@example.com"), "roleId": builtinRoleID}
	expectMessage(t, api.call(http.MethodPost, "/rbac/grants", body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodDelete, "/rbac/grants/"+missingID, nil), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}

func subSubject(sub string) map[string]any {
	return map[string]any{"type": "sub", "value": sub, "issuer": "https://idp.example.com"}
}

func TestGrants_UpdateSubject(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("re-recorded")
	api.seedUser(userSeed{sub: "alice-sub", email: "alice@example.com"})
	created := api.createGrant(emailSubject("alice@example.com"), role.ID)
	if created.State == nil || *created.State != entplatform.GrantStateConcrete {
		t.Fatalf("state %v, want concrete", created.State)
	}

	rec := api.call(http.MethodPatch, "/rbac/grants/"+created.ID, map[string]any{"subject": subSubject("alice-sub")})
	expectStatus(t, rec, http.StatusOK)
	updated := decodeData[Grant](t, rec)

	if updated.ID != created.ID || updated.RoleID != created.RoleID || updated.Type != "grant" || updated.Origin != entplatform.RBACOriginCustom {
		t.Errorf("updated grant %+v, want id, role, type and origin of %+v", updated, created)
	}
	if updated.UserID == nil || created.UserID == nil || *updated.UserID != *created.UserID {
		t.Errorf("userId %v, want %v", updated.UserID, created.UserID)
	}
	if updated.State == nil || *updated.State != entplatform.GrantStateConcrete {
		t.Errorf("state %v, want concrete", updated.State)
	}
	if s := updated.Subject; s == nil || s.Type != "sub" || s.Value != "alice-sub" || s.Issuer == nil || *s.Issuer != "https://idp.example.com" {
		t.Errorf("subject %+v, want the sub subject", s)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy == "" {
		t.Error("updatedBy is null after an update")
	}
	if updated.UpdatedTime < updated.CreatedTime || updated.CreatedTime != created.CreatedTime {
		t.Errorf("createdTime %v and updatedTime %v, want createdTime kept and updatedTime not before it", updated.CreatedTime, updated.UpdatedTime)
	}

	got := decodeData[Grant](t, api.call(http.MethodGet, "/rbac/grants/"+created.ID, nil))
	if got.Subject == nil || got.Subject.Type != "sub" || got.Subject.Value != "alice-sub" {
		t.Errorf("GET after the update has subject %+v", got.Subject)
	}

	rec = api.call(http.MethodPatch, "/rbac/grants/"+created.ID, map[string]any{"subject": emailSubject("alice@example.com")})
	expectStatus(t, rec, http.StatusOK)
	if s := decodeData[Grant](t, rec).Subject; s == nil || s.Type != "email" {
		t.Errorf("subject %+v after recording the email again", s)
	}
}

const (
	msgPendingGrantSubject    = "A pending grant has no user yet, so its subject cannot be changed. Delete it and create the grant again"
	msgSystemUserGrantSubject = "A service account's grant is recorded under the service account; change its roles through the service account instead"
)

func expectDetailKeys(t *testing.T, rec *httptest.ResponseRecorder, keys ...string) {
	t.Helper()
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if got := slices.Sorted(maps.Keys(decodeError(t, rec).ValidationDetails)); !slices.Equal(got, keys) {
		t.Errorf("validationDetails keys %v, want %v: %s", got, keys, rec.Body.String())
	}
}

func TestGrants_UpdateValidation(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	role := api.createRole("validated-update")
	api.seedUser(userSeed{sub: "carol-sub", email: "carol@example.com"})
	api.seedUser(userSeed{sub: "dave-sub", email: "dave@example.com"})
	grant := api.createGrant(emailSubject("carol@example.com"), role.ID)
	patch := func(body any) *httptest.ResponseRecorder {
		return api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, body)
	}

	expectDetailKeys(t, patch(map[string]any{"subject": emailSubject("dave@example.com")}), "subject")
	expectDetailKeys(t, patch(map[string]any{"subject": subSubject("dave-sub")}), "subject")
	expectDetailKeys(t, patch(`{"subject":null}`), "subject")
	expectDetailKeys(t, patch(`{"subject":{}}`), "subject")
	expectDetailKeys(t, patch(map[string]any{"subject": map[string]any{"type": "bogus", "value": "x"}}), "subject")
	expectDetailKeys(t, patch(map[string]any{"subject": map[string]any{"type": "email"}}), "subject")
	expectDetailKeys(t, patch(map[string]any{"subject": map[string]any{"type": "sub", "value": "carol-sub"}}), "subject")
	expectDetailKeys(t, patch(map[string]any{"subject": map[string]any{"type": "email", "value": "   "}}), "subject")
	expectDetailKeys(t, patch(`{"subject":{"type":"email","value":"carol@example.com"},"roleId":"`+role.ID+`"}`), "roleId")
	expectDetailKeys(t, patch(`{"userId":"`+missingID+`"}`), "userId")
	expectDetailKeys(t, patch(`{"subject":null,"roleId":"`+role.ID+`","userId":"`+missingID+`"}`), "roleId", "subject", "userId")

	whitespace := patch(map[string]any{"subject": map[string]any{"type": "email", "value": "   "}})
	if problems := decodeError(t, whitespace).ValidationDetails["subject"]; !slices.Contains(problems, "value cannot be empty") {
		t.Errorf("subject problems %v, want one saying value cannot be empty", problems)
	}

	got := decodeData[Grant](t, api.call(http.MethodGet, "/rbac/grants/"+grant.ID, nil))
	if got.Subject == nil || got.Subject.Value != "carol@example.com" {
		t.Errorf("a refused update changed the subject to %+v", got.Subject)
	}

	expectDetail(t, api.call(http.MethodPatch, "/rbac/grants/not-a-uuid", map[string]any{"subject": emailSubject("carol@example.com")}), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodPatch, "/rbac/grants/"+missingID, map[string]any{"subject": emailSubject("carol@example.com")}), http.StatusNotFound)
}

func TestGrants_UpdateRefusesGrantsItCannotRecord(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"alice@example.com": {Roles: []string{"yaml_viewer"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	role := api.createRole("not-recordable")

	yamlGrantID := entplatform.YAMLRowID(entplatform.YAMLKindGrant, "alice@example.com|yaml_viewer")
	expectMessage(t, api.call(http.MethodPatch, "/rbac/grants/"+yamlGrantID, map[string]any{"subject": emailSubject("alice@example.com")}),
		http.StatusConflict, "Grants defined in your configuration cannot be changed through the API")

	pending := api.createGrant(emailSubject("nobody-yet@example.com"), role.ID)
	expectMessage(t, api.call(http.MethodPatch, "/rbac/grants/"+pending.ID, map[string]any{"subject": emailSubject("nobody-yet@example.com")}),
		http.StatusConflict, msgPendingGrantSubject)

	su := api.createSystemUser("update-bot", role.ID)
	systemGrant := api.createGrant(systemUserSubject(su), api.createRole("extra").ID)
	expectMessage(t, api.call(http.MethodPatch, "/rbac/grants/"+systemGrant.ID, map[string]any{"subject": systemUserSubject(su)}),
		http.StatusConflict, msgSystemUserGrantSubject)
}

func TestGrants_UpdateRefusesASubjectTheConfigurationAlreadyGives(t *testing.T) {
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{"carol@example.com": {Roles: []string{"configured-role"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	role := api.createRole("configured-role")
	api.seedUser(userSeed{sub: "dave-sub", email: "dave@example.com"})
	grant := api.createGrant(emailSubject("dave@example.com"), role.ID)

	expectMessage(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{"subject": emailSubject("carol@example.com")}),
		http.StatusConflict, `Grant (identity="carol@example.com", role="configured-role") is defined in your configuration`)
}

func TestGrants_UpdateWithNoSubjectChangesNothing(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("untouched")
	api.seedUser(userSeed{sub: "ivy-sub", email: "ivy@example.com"})
	grant := api.createGrant(emailSubject("ivy@example.com"), role.ID)

	rec := api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{})

	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Grant](t, rec); got.ID != grant.ID || got.Subject == nil || got.Subject.Value != "ivy@example.com" {
		t.Errorf("an empty update answered %+v, want the grant unchanged", got)
	}
}

func TestGrants_UpdateRefusesBuiltinSystemUserSubject(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("builtin-subject")
	api.seedUser(userSeed{sub: "gina-sub", email: "gina@example.com"})
	grant := api.createGrant(emailSubject("gina@example.com"), role.ID)

	rec := api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{"subject": map[string]any{"type": "legacy", "value": rbac.DefaultSystemUserSub}})
	expectMessage(t, rec, http.StatusConflict, msgBuiltinSystemUser)
}

func TestGrants_UpdateNormalizesEmailSubject(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("normalized-subject")
	api.seedUser(userSeed{sub: "hal-sub", email: "hal@example.com"})
	grant := api.createGrant(subSubject("hal-sub"), role.ID)

	rec := api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{"subject": emailSubject("  Hal@Example.COM ")})
	expectStatus(t, rec, http.StatusOK)
	if s := decodeData[Grant](t, rec).Subject; s == nil || s.Type != "email" || s.Value != "hal@example.com" {
		t.Errorf("subject %+v, want the normalized email", s)
	}
}

func TestGrants_UpdateScopes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("update-scoped")
	api.seedUser(userSeed{sub: "erin-sub", email: "erin@example.com"})
	grant := api.createGrant(emailSubject("erin@example.com"), role.ID)
	body := map[string]any{"subject": subSubject("erin-sub")}

	expectMessage(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, body, rbac.ScopeRBACRead), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, body, rbac.ScopeConnectorRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, body), http.StatusOK)
}

func TestGrants_UpdateMiniIDPRefuses(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	body := map[string]any{"subject": emailSubject("a@example.com")}
	expectMessage(t, api.call(http.MethodPatch, "/rbac/grants/"+missingID, body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}
