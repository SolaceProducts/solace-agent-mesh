// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type userSeed struct {
	sub, displayName, email, userName string
	groups                            []string
	claims                            []store.UserIdentityClaim
}

func (a *entrypointAPI) seedUser(seed userSeed) string {
	a.t.Helper()
	u := &store.User{
		Issuer: "https://idp.example.com", Sub: seed.sub, OIDCProvider: "okta", Source: "{}",
		DisplayName: NullIfZero(seed.displayName), Email: NullIfZero(seed.email), UserName: NullIfZero(seed.userName),
		CreatedBy: "test", UpdatedBy: "test",
	}
	write := store.IdentityClaimWrite{Replace: len(seed.claims) > 0, Claims: seed.claims}
	if err := store.NewUsersRepo(a.core.DB()).UpsertUserWithClaims(a.t.Context(), u, seed.groups, write); err != nil {
		a.t.Fatalf("seed user %s: %v", seed.sub, err)
	}
	return u.ID
}

func (a *entrypointAPI) forgetLastLogin(userID string) {
	a.t.Helper()
	if _, err := a.core.DB().ExecContext(a.t.Context(), `UPDATE users SET last_login_at = NULL WHERE id = ?`, userID); err != nil {
		a.t.Fatalf("clear last login: %v", err)
	}
}

func (a *entrypointAPI) userRoleGrants(userID string) []string {
	a.t.Helper()
	rec := a.call(http.MethodGet, "/users/"+userID+"/roleGrants", nil)
	expectStatus(a.t, rec, http.StatusOK)
	grants := decodeData[UserRoleGrants](a.t, rec)
	slices.Sort(grants.RoleIDs)
	return grants.RoleIDs
}

func (a *entrypointAPI) replaceUserRoleGrants(userID string, roleIDs ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	return a.call(http.MethodPut, "/users/"+userID+"/roleGrants", map[string]any{"roleIds": nonNilList(roleIDs)})
}

func (a *entrypointAPI) grantIDs(userID string, roleIDs ...string) map[string]string {
	a.t.Helper()
	ids := map[string]string{}
	for _, roleID := range roleIDs {
		rec := a.call(http.MethodGet, "/rbac/grants?state=concrete&roleId="+roleID, nil)
		expectStatus(a.t, rec, http.StatusOK)
		for _, g := range decodeList[Grant](a.t, rec).Data {
			if g.UserID != nil && *g.UserID == userID {
				ids[roleID] = g.ID
			}
		}
	}
	return ids
}

func userDisplayNames(users []User) []string {
	out := make([]string, len(users))
	for i, u := range users {
		if u.DisplayName != nil {
			out[i] = *u.DisplayName
		}
	}
	return out
}

func TestUsers_ListAndGet(t *testing.T) {
	api := newRBACAPI(t)
	aliceID := api.seedUser(userSeed{sub: "u-alice", displayName: "Zq Alice", email: "alice@zq.example.com", groups: []string{"eng", "support"}})
	api.seedUser(userSeed{sub: "u-bob", displayName: "Zq Bob", email: "bob@zq.example.com", userName: "bobby"})
	api.seedUser(userSeed{sub: "u-carol", displayName: "Zq Carol", email: "carol@zq.example.com"})
	systemID := api.createSystemUser("zq-bot", builtinRoleID).ID
	api.forgetLastLogin(systemID)

	rec := api.call(http.MethodGet, "/users/"+aliceID, nil)
	expectStatus(t, rec, http.StatusOK)
	alice := decodeData[User](t, rec)
	if alice.ID != aliceID || alice.Type != "user" || alice.ProvisionedVia != "jit" || !alice.Active {
		t.Errorf("alice = %+v", alice)
	}
	if alice.DisplayName == nil || *alice.DisplayName != "Zq Alice" || alice.Email == nil || *alice.Email != "alice@zq.example.com" {
		t.Errorf("alice names = %v %v", alice.DisplayName, alice.Email)
	}
	groups := slices.Clone(alice.Groups)
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"eng", "support"}) {
		t.Errorf("groups %v, want [eng support]", alice.Groups)
	}
	raw := rawData(t, rec)
	if v, ok := raw["userName"]; !ok || v != nil {
		t.Errorf("userName of a user with none = %v (present %v), want null", v, ok)
	}
	for _, field := range []string{"budget", "budgetTopUp", "effectiveBudgetAmount"} {
		if v, ok := raw[field]; !ok || v != nil {
			t.Errorf("%s without expand = %v (present %v), want null", field, v, ok)
		}
	}
	for _, field := range []string{"createdTime", "updatedTime", "lastLoginTime"} {
		if s, _ := raw[field].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
			t.Errorf("%s = %v, want ISO 8601 ms UTC", field, raw[field])
		}
	}
	for _, v1Only := range []string{"createdAt", "updatedAt", "lastLoginAt", "phone", "source", "externalId"} {
		if _, ok := raw[v1Only]; ok {
			t.Errorf("field %s is on the v2 user", v1Only)
		}
	}
	if roles, ok := raw["roles"].([]any); !ok || roles == nil {
		t.Errorf("roles = %v, want an array", raw["roles"])
	}

	rec = api.call(http.MethodGet, "/users/"+systemID, nil)
	expectStatus(t, rec, http.StatusOK)
	system := decodeData[User](t, rec)
	if system.ProvisionedVia != "system" || system.LastLoginTime != 0 {
		t.Errorf("system user = %+v", system)
	}
	if v, ok := rawData(t, rec)["lastLoginTime"]; !ok || v != nil {
		t.Errorf("lastLoginTime of a user who never signed in = %v (present %v), want null", v, ok)
	}
	if want := (UserRole{RoleID: builtinRoleID, Source: rbac.SourceDirect, Manageable: true}); !slices.Contains(system.Roles, want) {
		t.Errorf("system user roles = %+v", system.Roles)
	}

	names := func(query string) ([]string, int) {
		t.Helper()
		rec := api.call(http.MethodGet, "/users?search=zq&"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		page := decodeList[User](t, rec)
		return userDisplayNames(page.Data), page.Meta.Pagination.Count
	}
	all := []string{"Zq Alice", "Zq Bob", "Zq Carol"}
	if got, total := names("sort=displayName:asc"); total != 3 || !slices.Equal(got, all) {
		t.Errorf("displayName asc = %v of %d", got, total)
	}
	if got, _ := names("sort=displayName:desc"); !slices.Equal(got, []string{"Zq Carol", "Zq Bob", "Zq Alice"}) {
		t.Errorf("displayName desc = %v", got)
	}
	if got, _ := names("sort=email:desc"); !slices.Equal(got, []string{"Zq Carol", "Zq Bob", "Zq Alice"}) {
		t.Errorf("email desc = %v", got)
	}
	if got, total := names("sort=displayName:asc&pageSize=2"); total != 3 || !slices.Equal(got, all[:2]) {
		t.Errorf("page 1 = %v of %d", got, total)
	}
	if got, _ := names("sort=displayName:asc&pageSize=2&pageNumber=2"); !slices.Equal(got, all[2:]) {
		t.Errorf("page 2 = %v", got)
	}
	if got, _ := names("pageSize=2&pageNumber=3"); len(got) != 0 {
		t.Errorf("page past the end = %v, want []", got)
	}
	if got, _ := names("group=eng"); !slices.Equal(got, []string{"Zq Alice"}) {
		t.Errorf("group filter = %v", got)
	}
	if got, _ := names("oidcProvider=okta"); len(got) != 3 {
		t.Errorf("oidcProvider filter = %v", got)
	}

	expectDetail(t, api.call(http.MethodGet, "/users?sort=phone:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/users?sort=lastLoginAt:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/users?roleId=sam_manager", nil), http.StatusBadRequest, "roleId")
	expectStatus(t, api.call(http.MethodGet, "/users/"+missingID, nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/users/not-a-uuid", nil), http.StatusBadRequest, "id")
}

func (a *entrypointAPI) setUserTimes(userID string, lastLogin, created, updated int64) {
	a.t.Helper()
	if _, err := a.core.DB().ExecContext(a.t.Context(),
		`UPDATE users SET last_login_at = ?, created_time = ?, updated_time = ? WHERE id = ?`, lastLogin, created, updated, userID); err != nil {
		a.t.Fatalf("set user times: %v", err)
	}
}

func TestUsers_SortsByTime(t *testing.T) {
	api := newRBACAPI(t)
	alice := api.seedUser(userSeed{sub: "u-alice", displayName: "Zq Alice", email: "alice@zq.example.com"})
	bob := api.seedUser(userSeed{sub: "u-bob", displayName: "Zq Bob", email: "bob@zq.example.com"})
	carol := api.seedUser(userSeed{sub: "u-carol", displayName: "Zq Carol", email: "carol@zq.example.com"})
	const base = int64(1_790_000_000_000)
	api.setUserTimes(alice, base+2, base+3, base+1)
	api.setUserTimes(bob, base+3, base+1, base+2)
	api.setUserTimes(carol, base+1, base+2, base+3)

	ids := func(query string) []string {
		t.Helper()
		rec := api.call(http.MethodGet, "/users?search=zq&"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		page := decodeList[User](t, rec)
		out := make([]string, len(page.Data))
		for i, u := range page.Data {
			out[i] = u.ID
		}
		return out
	}
	for field, ascending := range map[string][]string{
		"lastLoginTime": {carol, alice, bob},
		"createdTime":   {bob, carol, alice},
		"updatedTime":   {alice, bob, carol},
	} {
		if got := ids("sort=" + field + ":asc"); !slices.Equal(got, ascending) {
			t.Errorf("%s asc = %v, want %v", field, got, ascending)
		}
		descending := slices.Clone(ascending)
		slices.Reverse(descending)
		if got := ids("sort=" + field + ":desc"); !slices.Equal(got, descending) {
			t.Errorf("%s desc = %v, want %v", field, got, descending)
		}
	}
}

func TestUsers_ExpandedBudgetAndTopUp(t *testing.T) {
	api := newRBACAPI(t)
	ctx := t.Context()
	db := api.core.DB()
	funded := api.seedUser(userSeed{sub: "u-funded", displayName: "Zq Funded", email: "funded@zq.example.com"})
	other := api.seedUser(userSeed{sub: "u-other", displayName: "Zq Other", email: "other@zq.example.com"})

	limit := budgetAmount(t, "250.00")
	team := &store.Budget{Name: "Zq team", SubjectType: store.BudgetSubjectUser, Amount: &limit, CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewBudgetsRepo(db).Create(ctx, team); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	assignment := &store.BudgetAssignment{SubjectType: store.BudgetSubjectUser, SubjectRef: funded, BudgetID: team.ID, CreatedBy: "test", UpdatedBy: "test"}
	if _, err := store.NewBudgetAssignmentsRepo(db).Create(ctx, assignment); err != nil {
		t.Fatalf("assign budget: %v", err)
	}
	expires := time.Now().Add(24 * time.Hour).UnixMilli()
	_, err := store.NewBudgetTopUpsRepo(db).Grant(ctx, store.BudgetTopUpGrant{
		SubjectType: store.BudgetSubjectUser, SubjectRef: funded, Amount: budgetAmount(t, "50.00"),
		ExpiresTime: expires, Actor: "test", NowMs: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatalf("grant top-up: %v", err)
	}
	withBudget := []string{rbac.ScopeRBACRead, rbac.ScopeBudgetRead}

	check := func(name string, u User) {
		t.Helper()
		if u.Budget == nil || u.Budget.ID != team.ID || u.Budget.Name != "Zq team" || u.Budget.Amount == nil || *u.Budget.Amount != "250.00" ||
			u.Budget.Source != "assigned" || u.Budget.AssignmentID == nil || *u.Budget.AssignmentID != assignment.ID {
			t.Errorf("%s budget = %+v, want Zq team at 250.00 through assignment %s", name, u.Budget, assignment.ID)
		}
		if u.BudgetTopUp == nil || u.BudgetTopUp.Amount != "50.00" || int64(u.BudgetTopUp.ExpiresTime) != expires {
			t.Errorf("%s budgetTopUp = %+v, want 50.00 expiring at %d", name, u.BudgetTopUp, expires)
		}
		if u.EffectiveBudgetAmount == nil || *u.EffectiveBudgetAmount != "300.00" {
			t.Errorf("%s effectiveBudgetAmount = %q, want 300.00", name, orEmpty(u.EffectiveBudgetAmount))
		}
	}
	rec := api.call(http.MethodGet, "/users/"+funded+"?expand=budget", nil, withBudget...)
	expectStatus(t, rec, http.StatusOK)
	check("GET", decodeData[User](t, rec))
	if topUp, ok := rawData(t, rec)["budgetTopUp"].(map[string]any); !ok {
		t.Errorf("budgetTopUp = %v, want an object", rawData(t, rec)["budgetTopUp"])
	} else {
		expectAbsent(t, topUp, "id", "type")
	}

	rec = api.call(http.MethodGet, "/users?expand=budget&search=funded", nil, withBudget...)
	expectStatus(t, rec, http.StatusOK)
	if page := decodeList[User](t, rec).Data; len(page) != 1 {
		t.Errorf("expanded list = %d users, want 1", len(page))
	} else {
		check("list", page[0])
	}

	rec = api.call(http.MethodGet, "/users?search=zq&budgetId="+team.ID, nil, withBudget...)
	expectStatus(t, rec, http.StatusOK)
	page := decodeList[User](t, rec).Data
	if len(page) != 1 || page[0].ID != funded {
		t.Errorf("users on the budget = %+v, want only %s and not %s", page, funded, other)
	}
}

func budgetAmount(t *testing.T, decimal string) int64 {
	t.Helper()
	amount, err := budget.ParseAmount(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return amount
}

func TestUsers_RoleGrantsReplaceKeepsGrantIDs(t *testing.T) {
	api := newRBACAPI(t)
	userID := api.seedUser(userSeed{sub: "u-grants", email: "grants@example.com"})
	a, b, c := api.createRole("grant-a").ID, api.createRole("grant-b").ID, api.createRole("grant-c").ID

	if got := api.userRoleGrants(userID); len(got) != 0 {
		t.Fatalf("new user has grants %v", got)
	}
	rec := api.replaceUserRoleGrants(userID, a, b)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[UserRoleGrants](t, rec).RoleIDs; !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values([]string{a, b}))) {
		t.Errorf("PUT answered %v, want %v", got, []string{a, b})
	}
	if got, want := api.userRoleGrants(userID), slices.Sorted(slices.Values([]string{a, b})); !slices.Equal(got, want) {
		t.Errorf("grants %v, want %v", got, want)
	}
	before := api.grantIDs(userID, a, b)
	if before[a] == "" || before[b] == "" {
		t.Fatalf("grant ids %v, want one for each of a and b", before)
	}

	expectStatus(t, api.replaceUserRoleGrants(userID, a, c), http.StatusOK)
	after := api.grantIDs(userID, a, b, c)
	if after[a] != before[a] {
		t.Errorf("the grant of a changed from %s to %s", before[a], after[a])
	}
	if after[b] != "" || after[c] == "" {
		t.Errorf("grants after [a c] = %v, want a and c only", after)
	}

	user := decodeData[User](t, api.call(http.MethodGet, "/users/"+userID, nil))
	var direct []string
	for _, r := range user.Roles {
		if r.Source == rbac.SourceDirect {
			direct = append(direct, r.RoleID)
			if !r.Manageable {
				t.Errorf("direct role %s is not manageable", r.RoleID)
			}
		}
	}
	slices.Sort(direct)
	if want := slices.Sorted(slices.Values([]string{a, c})); !slices.Equal(direct, want) {
		t.Errorf("direct roles on the user %v, want %v", direct, want)
	}

	expectStatus(t, api.replaceUserRoleGrants(userID), http.StatusOK)
	if got := api.userRoleGrants(userID); len(got) != 0 {
		t.Errorf("grants after [] = %v", got)
	}
	expectStatus(t, api.replaceUserRoleGrants(userID, b), http.StatusOK)
	expectStatus(t, api.call(http.MethodPut, "/users/"+userID+"/roleGrants", `{}`), http.StatusOK)
	if got := api.userRoleGrants(userID); len(got) != 0 {
		t.Errorf("grants after a body without roleIds = %v", got)
	}
}

func TestUsers_RoleGrantsValidation(t *testing.T) {
	api := newRBACAPI(t, withRBACYAML(testRBACYAML))
	userID := api.seedUser(userSeed{sub: "u-validate", email: "validate@example.com"})
	yamlRoleID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")

	expectDetail(t, api.replaceUserRoleGrants(userID, missingID), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.replaceUserRoleGrants(userID, yamlRoleID), http.StatusUnprocessableEntity, "roleIds")
	expectDetail(t, api.call(http.MethodPut, "/users/not-a-uuid/roleGrants", map[string]any{"roleIds": []string{}}), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodGet, "/users/not-a-uuid/roleGrants", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.replaceUserRoleGrants(missingID), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/users/"+missingID+"/roleGrants", nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodPut, "/users/"+userID+"/roleGrants", `{"roleIds":"x"}`), http.StatusBadRequest, "roleIds")
	expectDetail(t, api.call(http.MethodPut, "/users/"+userID+"/roleGrants", `{"roles":[]}`), http.StatusBadRequest, "roles")
	if got := api.userRoleGrants(userID); len(got) != 0 {
		t.Errorf("a refused write left grants %v", got)
	}
}

func TestUsers_RolesReferenceYAMLRolesByV2ID(t *testing.T) {
	const email = "yamlalice@example.com"
	state := testRBACYAML
	state.Users = rbac.UsersConfig{Users: map[string]rbac.UserAssignment{email: {Roles: []string{"yaml_viewer"}}}}
	api := newRBACAPI(t, withRBACYAML(state))
	userID := api.seedUser(userSeed{sub: "u-yaml", displayName: "Yaml Alice", email: email})
	api.seedUser(userSeed{sub: "u-plain", displayName: "Plain Pat", email: "plain@example.com"})
	viewerID := entplatform.YAMLRowID(entplatform.YAMLKindRole, "yaml_viewer")

	user := decodeData[User](t, api.call(http.MethodGet, "/users/"+userID, nil))
	var found *UserRole
	for i, r := range user.Roles {
		if r.RoleID == viewerID {
			found = &user.Roles[i]
		}
	}
	if found == nil || found.Source != rbac.SourceYAML || found.Manageable {
		t.Fatalf("roles %+v, want yaml_viewer by v2 id %s, source yaml, not manageable", user.Roles, viewerID)
	}

	rec := api.call(http.MethodGet, "/users?roleId="+viewerID, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := userDisplayNames(decodeList[User](t, rec).Data); !slices.Equal(got, []string{"Yaml Alice"}) {
		t.Errorf("users with the YAML role = %v, want [Yaml Alice]", got)
	}
	rec = api.call(http.MethodGet, "/users?roleId="+missingID, nil)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[User](t, rec).Data; len(got) != 0 {
		t.Errorf("users with an unknown role = %d, want none", len(got))
	}
}

func TestUsers_IdentityClaims(t *testing.T) {
	api := newRBACAPI(t)
	bareID := api.seedUser(userSeed{sub: "u-bare", email: "bare@example.com"})
	claimedID := api.seedUser(userSeed{sub: "u-claimed", email: "claimed@example.com", claims: []store.UserIdentityClaim{
		{ClaimKey: "phone_number", ClaimValueCanonical: "+15551234567", Verified: true},
		{ClaimKey: "email", ClaimValueCanonical: "claimed@example.com"},
	}})

	rec := api.call(http.MethodGet, "/users/"+bareID+"/identityClaims", nil)
	expectStatus(t, rec, http.StatusOK)
	if page := decodeList[UserIdentityClaim](t, rec); len(page.Data) != 0 || page.Meta.Pagination.Count != 0 {
		t.Errorf("claims of a user with none = %+v", page)
	}

	rec = api.call(http.MethodGet, "/users/"+claimedID+"/identityClaims", nil)
	expectStatus(t, rec, http.StatusOK)
	page := decodeList[UserIdentityClaim](t, rec)
	if page.Meta.Pagination.Count != 2 || len(page.Data) != 2 {
		t.Fatalf("claims = %+v, want 2", page)
	}
	byKey := map[string]UserIdentityClaim{}
	for _, c := range page.Data {
		byKey[c.ClaimKey] = c
	}
	if phone := byKey["phone_number"]; phone.ClaimValueCanonical != "+15551234567" || !phone.Verified || phone.UpdatedTime == 0 {
		t.Errorf("phone claim = %+v", phone)
	}
	var rawPage struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rawPage); err != nil {
		t.Fatal(err)
	}
	if s, _ := rawPage.Data[0]["updatedTime"].(string); !regexp.MustCompile(isoMillisUTC).MatchString(s) {
		t.Errorf("updatedTime = %v, want ISO 8601 ms UTC", rawPage.Data[0]["updatedTime"])
	}
	if got := len(decodeList[UserIdentityClaim](t, api.call(http.MethodGet, "/users/"+claimedID+"/identityClaims?pageSize=1", nil)).Data); got != 1 {
		t.Errorf("page of 1 holds %d claims", got)
	}
	if got := decodeList[UserIdentityClaim](t, api.call(http.MethodGet, "/users/"+claimedID+"/identityClaims?pageSize=1&pageNumber=3", nil)).Data; len(got) != 0 {
		t.Errorf("page past the end = %v, want []", got)
	}

	expectStatus(t, api.call(http.MethodDelete, "/users/"+claimedID+"/identityClaims", nil), http.StatusNoContent)
	if got := decodeList[UserIdentityClaim](t, api.call(http.MethodGet, "/users/"+claimedID+"/identityClaims", nil)).Data; len(got) != 0 {
		t.Errorf("claims after revoke = %v", got)
	}
	expectStatus(t, api.call(http.MethodDelete, "/users/"+claimedID+"/identityClaims", nil), http.StatusNoContent)

	expectStatus(t, api.call(http.MethodGet, "/users/"+missingID+"/identityClaims", nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/users/"+missingID+"/identityClaims", nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/users/not-a-uuid/identityClaims", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodDelete, "/users/not-a-uuid/identityClaims", nil), http.StatusBadRequest, "id")
}

func TestUsers_IdentityClaimRoutesIgnoreTheRBACFlag(t *testing.T) {
	t.Setenv(features.EnvVarName("rbac"), "false")
	api := newRBACAPI(t)
	userID := api.seedUser(userSeed{sub: "u-flag", email: "flag@example.com"})

	expectStatus(t, api.call(http.MethodGet, "/users", nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/users/"+userID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/users/"+userID+"/roleGrants", nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/users/"+userID+"/identityClaims", nil), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, "/users/"+userID+"/identityClaims", nil), http.StatusNoContent)
}

func TestUsers_Scopes(t *testing.T) {
	api := newRBACAPI(t)
	userID := api.seedUser(userSeed{sub: "u-scopes", email: "scopes@example.com"})
	role := api.createRole("scoped-grant")
	reader := rbac.ScopeRBACRead

	for _, path := range []string{"/users", "/users/" + userID, "/users/" + userID + "/roleGrants", "/users/" + userID + "/identityClaims"} {
		expectStatus(t, api.call(http.MethodGet, path, nil, reader), http.StatusOK)
		expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeConnectorRead), http.StatusForbidden)
	}
	body := map[string]any{"roleIds": []string{role.ID}}
	expectMessage(t, api.call(http.MethodPut, "/users/"+userID+"/roleGrants", body, reader), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodPut, "/users/"+userID+"/roleGrants", body, "rbac:*:*"), http.StatusForbidden, msgNotSuperuser)
	expectMessage(t, api.call(http.MethodDelete, "/users/"+userID+"/identityClaims", nil, reader), http.StatusForbidden, msgNotSuperuser)
	expectStatus(t, api.call(http.MethodPut, "/users/"+userID+"/roleGrants", body, "*:*:*"), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, "/users/"+userID+"/identityClaims", nil, "*:*:*"), http.StatusNoContent)
}

func TestUsers_BudgetNeedsBudgetRead(t *testing.T) {
	api := newRBACAPI(t)
	userID := api.seedUser(userSeed{sub: "u-budget", email: "budget@example.com"})
	reader := rbac.ScopeRBACRead

	for _, query := range []string{"?budgetId=" + missingID, "?expand=budget"} {
		expectMessage(t, api.call(http.MethodGet, "/users"+query, nil, reader), http.StatusForbidden, msgNoPermission)
	}
	expectMessage(t, api.call(http.MethodGet, "/users/"+userID+"?expand=budget", nil, reader), http.StatusForbidden, msgNoPermission)

	withBudget := []string{reader, rbac.ScopeBudgetRead}
	rec := api.call(http.MethodGet, "/users/"+userID+"?expand=budget", nil, withBudget...)
	expectStatus(t, rec, http.StatusOK)
	expandedUser := rawData(t, rec)
	for _, field := range []string{"budgetTopUp", "effectiveBudgetAmount"} {
		if _, ok := expandedUser[field]; !ok {
			t.Errorf("%s is missing from an expanded user", field)
		}
	}
	if v, ok := expandedUser["budget"]; !ok {
		t.Errorf("budget is missing from an expanded user")
	} else if v != nil {
		if _, isObject := v.(map[string]any); !isObject {
			t.Errorf("expanded budget = %v, want null or an object", v)
		}
	}
	rec = api.call(http.MethodGet, "/users?expand=budget&search=budget", nil, withBudget...)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[User](t, rec); len(got.Data) != 1 {
		t.Errorf("expanded list = %d users, want 1", len(got.Data))
	}
	rec = api.call(http.MethodGet, "/users?budgetId="+missingID, nil, withBudget...)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeList[User](t, rec).Data; len(got) != 0 {
		t.Errorf("users on an unknown budget = %d, want none", len(got))
	}
	expectDetail(t, api.call(http.MethodGet, "/users?budgetId=nope", nil, withBudget...), http.StatusBadRequest, "budgetId")
	expectDetail(t, api.call(http.MethodGet, "/users?expand=roles", nil, withBudget...), http.StatusUnprocessableEntity, "expand")
	expectDetail(t, api.call(http.MethodGet, "/users/"+userID+"?expand=roles", nil, withBudget...), http.StatusUnprocessableEntity, "expand")
}

func TestUsers_MiniIDPRefusesWrites(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	userID := api.seedUser(userSeed{sub: "u-mini", email: "mini@example.com"})
	expectStatus(t, api.call(http.MethodGet, "/users", nil), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/users/"+userID+"/identityClaims", nil), http.StatusOK)
	expectMessage(t, api.replaceUserRoleGrants(userID, builtinRoleID), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodDelete, "/users/"+userID+"/identityClaims", nil), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
}
