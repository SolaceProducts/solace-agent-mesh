// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func usPtr(s string) *string { return &s }

func newUsersTestService(t *testing.T) (*UsersService, *store.UsersRepo, *store.RBACRepo, *rbac.Composer) {
	t.Helper()
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	rbacRepo := store.NewRBACRepo(db)
	composer := rbac.NewComposer(embeddedYAMLState(t), newRBACDBSource(rbacRepo), slog.Default())
	svc := NewUsersService(usersRepo, rbacRepo, slog.Default())
	svc.SetGrantRefresh(composer, nil, nil)
	return svc, usersRepo, rbacRepo, composer
}

func embeddedYAMLState(t *testing.T) rbac.YAMLState {
	t.Helper()
	roles, users, err := rbac.EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}
	return rbac.YAMLState{Roles: roles, Users: users}
}

func roleIDsOf(roles []UserRoleAssignment) []string {
	seen := make(map[string]struct{}, len(roles))
	var ids []string
	for _, r := range roles {
		if _, dup := seen[r.RoleID]; dup {
			continue
		}
		seen[r.RoleID] = struct{}{}
		ids = append(ids, r.RoleID)
	}
	slices.Sort(ids)
	return ids
}

func seedRole(ctx context.Context, t *testing.T, repo *store.RBACRepo, composer *rbac.Composer, name string) string {
	t.Helper()
	return seedRoleWithScopes(ctx, t, repo, composer, name, nil)
}

func seedRoleWithScopes(ctx context.Context, t *testing.T, repo *store.RBACRepo, composer *rbac.Composer, name string, scopes []string) string {
	t.Helper()
	role := &store.RBACRole{Name: name, CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.CreateRole(ctx, role, scopes, nil); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild after seeding role %s: %v", name, err)
	}
	return role.ID
}

func grantRoles(ctx context.Context, t *testing.T, rbacRepo *store.RBACRepo, composer *rbac.Composer, byUser map[string]string) {
	t.Helper()
	for uid, rid := range byUser {
		if err := rbacRepo.AddUserRole(ctx, uid, rid, "x"); err != nil {
			t.Fatalf("assign role %s to %s: %v", rid, uid, err)
		}
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
}

func accessUserIDs(page *PaginatedResponse[ComponentAccessUser]) []string {
	ids := make([]string, 0, len(page.Data))
	for _, u := range page.Data {
		if !u.IsSystemUser {
			ids = append(ids, u.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

func listedSystemUsers() []rbac.ReservedSystemUser {
	return slices.DeleteFunc(rbac.ReservedSystemUsers(), func(r rbac.ReservedSystemUser) bool { return r.HiddenFromUserListings })
}

const invokeAnySystemUsers = 2

func seedUserRow(ctx context.Context, t *testing.T, repo *store.UsersRepo, sub, name, email, provider, source string, groups []string) string {
	t.Helper()
	u := &store.User{
		Issuer: "iss", Sub: sub, OIDCProvider: provider,
		DisplayName: usPtr(name), Email: usPtr(email), Source: source,
		CreatedBy: "x", UpdatedBy: "x",
	}
	if err := repo.UpsertUser(ctx, u, groups); err != nil {
		t.Fatalf("seed user %s: %v", sub, err)
	}
	return u.ID
}

func TestUsersService_ResolvesAssignedRoleIDs(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleDirect := seedRole(ctx, t, rbacRepo, composer, "direct")
	roleGroup := seedRole(ctx, t, rbacRepo, composer, "group")

	u1 := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure",
		`{"groups":["eng"],"department":"finance"}`, []string{"eng"})
	_ = seedUserRow(ctx, t, usersRepo, "s2", "Bob", "bob@example.com", "azure",
		`{"groups":["sales"]}`, []string{"sales"})

	if err := rbacRepo.AddUserRole(ctx, u1, roleDirect, "x"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", Name: "Engineers", CreatedBy: "x", UpdatedBy: "x"}, []string{roleGroup}); err != nil {
		t.Fatalf("claim mapping: %v", err)
	}

	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := svc.GetByID(ctx, u1)
	if err != nil {
		t.Fatalf("GetByID u1: %v", err)
	}
	want := []string{roleDirect, roleGroup}
	slices.Sort(want)
	if !slices.Equal(roleIDsOf(got.Roles), want) {
		t.Errorf("u1 roles = %v, want %v (direct + group-claim + non-group-claim)", got.Roles, want)
	}
	if got.DisplayName != "Alice" || got.Email != "alice@example.com" {
		t.Errorf("u1 displayName/email = %q/%q", got.DisplayName, got.Email)
	}
	if got.OIDCProvider != "azure" || got.LastLoginAt == 0 {
		t.Errorf("u1 = %+v, want provider azure + non-zero lastLoginAt", got)
	}
	if !slices.Contains(got.Groups, "eng") {
		t.Errorf("u1 groups = %v, want to contain eng", got.Groups)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, Search: "bob"})
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if len(page.Data) != 1 || len(page.Data[0].Roles) != 0 {
		t.Errorf("bob roles = %v, want empty", page.Data[0].Roles)
	}
}

func TestUsersService_RoleIDFilter(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleDirect := seedRole(ctx, t, rbacRepo, composer, "direct")
	roleGroup := seedRole(ctx, t, rbacRepo, composer, "group")
	other := seedRole(ctx, t, rbacRepo, composer, "unused")

	u1 := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", `{"groups":["eng"]}`, []string{"eng"})
	_ = seedUserRow(ctx, t, usersRepo, "s2", "Bob", "bob@example.com", "azure", `{"groups":["sales"]}`, []string{"sales"})

	if err := rbacRepo.AddUserRole(ctx, u1, roleDirect, "x"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", Name: "Engineers", CreatedBy: "x", UpdatedBy: "x"}, []string{roleGroup}); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: roleDirect})
	if err != nil {
		t.Fatalf("filter direct: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != u1 {
		t.Errorf("roleId=direct returned %d users, want 1 (u1)", len(page.Data))
	}

	page, err = svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: roleGroup})
	if err != nil {
		t.Fatalf("filter group: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != u1 {
		t.Errorf("roleId=group returned %d users, want 1 (u1)", len(page.Data))
	}

	page, err = svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: other})
	if err != nil {
		t.Fatalf("filter unused: %v", err)
	}
	if len(page.Data) != 0 || page.Meta.Pagination.Count != 0 {
		t.Errorf("roleId=unused returned %d users (count %d), want 0", len(page.Data), page.Meta.Pagination.Count)
	}
}

func TestUsersService_RoleIDFilterMatchesNonGroupClaim(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleDept := seedRole(ctx, t, rbacRepo, composer, "dept")
	u := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure",
		`{"department":"finance"}`, nil)
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{
		OIDCProvider: "azure", ClaimKey: "department", ClaimValue: "finance",
		Name: "Finance", CreatedBy: "x", UpdatedBy: "x",
	}, []string{roleDept}); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := svc.GetByID(ctx, u)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ids := roleIDsOf(got.Roles); len(ids) != 1 || ids[0] != roleDept {
		t.Fatalf("per-user view roles = %v, want [%s] (non-group claim)", got.Roles, roleDept)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: roleDept})
	if err != nil {
		t.Fatalf("filter dept: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != u {
		t.Errorf("roleId=dept returned %d users, want 1 (u); list filter diverged from per-user view", len(page.Data))
	}
}

func TestUsersService_RoleIDFilterBeyondInClauseCap(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	role := seedRole(ctx, t, rbacRepo, composer, "default-role")
	if err := rbacRepo.SetDefaultRoles(ctx, []string{role}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	const population = dbutil.InClauseCap + 1
	for i := range population {
		seedUserRow(ctx, t, usersRepo, fmt.Sprintf("s%d", i), fmt.Sprintf("User %04d", i), fmt.Sprintf("u%04d@example.com", i), "azure", "", nil)
	}
	special := seedUserRow(ctx, t, usersRepo, "special", "Zzz Special", "special@example.com", "okta", "", []string{"vip"})
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	const total = population + 1

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role})
	if err != nil {
		t.Fatalf("List with %d matching users: %v", total, err)
	}
	if page.Meta.Pagination.Count != total {
		t.Errorf("count = %d, want %d (every user inherits the default role)", page.Meta.Pagination.Count, total)
	}
	if len(page.Data) != 20 {
		t.Errorf("page size = %d, want 20", len(page.Data))
	}

	for _, tc := range []struct {
		name string
		q    UserListQuery
	}{
		{"oidcProvider", UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role, OIDCProvider: "okta"}},
		{"group", UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role, Group: "vip"}},
		{"search", UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role, Search: "special"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, listErr := svc.List(ctx, tc.q)
			if listErr != nil {
				t.Fatalf("List(%s): %v", tc.name, listErr)
			}
			if len(got.Data) != 1 || got.Data[0].ID != special {
				t.Errorf("List(%s) = %d users, want 1 (special)", tc.name, len(got.Data))
			}
		})
	}

	sorted, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 1, RoleID: role, SortBy: "displayName", SortOrder: "desc"})
	if err != nil {
		t.Fatalf("List(sort desc): %v", err)
	}
	if len(sorted.Data) != 1 || sorted.Data[0].ID != special {
		t.Errorf("List(sort desc) first = %+v, want special", sorted.Data)
	}

	otherRole := seedRole(ctx, t, rbacRepo, composer, "other-role")
	excluded := seedUserRow(ctx, t, usersRepo, "excluded", "Zzz Excluded", "excluded@example.com", "azure", "", nil)
	if assignErr := rbacRepo.AddUserRole(ctx, excluded, otherRole, "x"); assignErr != nil {
		t.Fatalf("assign other role: %v", assignErr)
	}
	if rebuildErr := composer.Rebuild(ctx); rebuildErr != nil {
		t.Fatalf("rebuild after excluded user: %v", rebuildErr)
	}
	notHolder, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role, Search: "excluded"})
	if err != nil {
		t.Fatalf("List(default-role, search=excluded): %v", err)
	}
	if len(notHolder.Data) != 0 {
		t.Errorf("List(default-role, search=excluded) = %+v, want empty (excluded holds a different role, not the default)", notHolder.Data)
	}
	holder, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: otherRole})
	if err != nil {
		t.Fatalf("List(other-role): %v", err)
	}
	if len(holder.Data) != 1 || holder.Data[0].ID != excluded {
		t.Errorf("List(other-role) = %+v, want just excluded", holder.Data)
	}
}

func TestUsersService_RoleIDFilterSortAscending(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	role := seedRole(ctx, t, rbacRepo, composer, "shared")
	uZ := seedUserRow(ctx, t, usersRepo, "s1", "Zed", "zed@example.com", "azure", "", nil)
	uA := seedUserRow(ctx, t, usersRepo, "s2", "Amy", "amy@example.com", "azure", "", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{uZ: role, uA: role})

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role, SortBy: "displayName", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("List(sort asc): %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].ID != uA || page.Data[1].ID != uZ {
		t.Errorf("List(sort asc) = %+v, want [Amy, Zed]", page.Data)
	}
}

func TestUsersService_RoleIDFilterUnknownRoleReturnsEmptyPage(t *testing.T) {
	t.Parallel()
	svc, usersRepo, _, _ := newUsersTestService(t)
	ctx := t.Context()
	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "a@example.com", "azure", "", nil)

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: "00000000-0000-0000-0000-000000000000"})
	if err != nil {
		t.Fatalf("List(unknown roleId): %v", err)
	}
	if len(page.Data) != 0 || page.Meta.Pagination.Count != 0 {
		t.Errorf("List(unknown roleId) = %d rows (count %d), want an empty page", len(page.Data), page.Meta.Pagination.Count)
	}
}

func TestUsersService_RoleIDFilterMatchesYAMLRole(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	rbacRepo := store.NewRBACRepo(db)
	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"yaml_admin": {Scopes: []string{"*"}},
		}},
		Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"alice@example.com": {Roles: []string{"yaml_admin"}},
		}},
	}
	composer := rbac.NewComposer(yaml, newRBACDBSource(rbacRepo), slog.Default())
	svc := NewUsersService(usersRepo, rbacRepo, slog.Default())
	svc.SetGrantRefresh(composer, nil, nil)
	ctx := t.Context()

	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "", nil)
	seedUserRow(ctx, t, usersRepo, "s2", "Bob", "bob@example.com", "azure", "", nil)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: "yaml_admin"})
	if err != nil {
		t.Fatalf("List(roleId=yaml_admin): %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].DisplayName != "Alice" {
		t.Errorf("List(roleId=yaml_admin) = %+v, want just Alice", page.Data)
	}
}

func TestUsersService_ListIncludesSeededSystemUsers(t *testing.T) {
	t.Parallel()
	svc, usersRepo, _, composer := newUsersTestService(t)
	ctx := t.Context()
	alice := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "", nil)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, SortBy: "email", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	reserved := listedSystemUsers()
	if want := 1 + len(reserved); len(page.Data) != want || page.Meta.Pagination.Count != want {
		t.Fatalf("List = %d data, count %d; want %d/%d (Alice + the seeded system users)", len(page.Data), page.Meta.Pagination.Count, want, want)
	}
	if last := page.Data[len(page.Data)-1]; last.ID != alice {
		t.Errorf("last row id = %s, want Alice (%s) after the no-email system users", last.ID, alice)
	}
	byID := make(map[string]UserResponse, len(page.Data))
	for _, u := range page.Data {
		byID[u.ID] = u
	}
	for _, r := range reserved {
		u, ok := byID[r.ID]
		if !ok || u.Sub != r.Sub || u.DisplayName != r.DisplayName || u.ProvisionedVia != rbac.ProvisionedViaSystem {
			t.Errorf("seeded %s = %+v (listed %v), want id %s, display name %q, provisioned via system", r.Sub, u, ok, r.ID, r.DisplayName)
		}
	}
	if got := roleIDsOf(byID[rbac.DefaultSystemUserID].Roles); !slices.Equal(got, []string{rbac.RoleSystemInvokeAll}) {
		t.Errorf("default row roles = %v, want [%s] resolved from embedded defaults", got, rbac.RoleSystemInvokeAll)
	}
	for _, r := range rbac.ReservedSystemUsers() {
		if !r.HiddenFromUserListings {
			continue
		}
		if _, listed := byID[r.ID]; listed {
			t.Errorf("%s is listed, want it hidden from the Users page", r.Sub)
		}
		if u, _, getErr := usersRepo.GetByID(ctx, r.ID); getErr != nil || u == nil || u.Sub != r.Sub {
			t.Errorf("hidden %s row = %+v (err %v), want it still seeded with id %s", r.Sub, u, getErr, r.ID)
		}
	}

	byRole, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: rbac.RoleSystemChannelInvoke})
	if err != nil {
		t.Fatalf("List(roleId=%s): %v", rbac.RoleSystemChannelInvoke, err)
	}
	if len(byRole.Data) != 0 || byRole.Meta.Pagination.Count != 0 {
		t.Errorf("List(roleId=%s) = %+v, want the hidden channel user left out", rbac.RoleSystemChannelInvoke, byRole.Data)
	}
}

func TestUsersService_ListExcludesDefaultWhenGroupOrOIDCProviderFiltered(t *testing.T) {
	t.Parallel()
	svc, usersRepo, _, _ := newUsersTestService(t)
	ctx := t.Context()
	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "", []string{"eng"})

	byGroup, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, Group: "eng"})
	if err != nil {
		t.Fatalf("List(group=eng): %v", err)
	}
	if len(byGroup.Data) != 1 || byGroup.Data[0].ID == rbac.DefaultSystemUserID {
		t.Errorf("List(group=eng) = %+v, want just Alice (no built-in default)", byGroup.Data)
	}

	byProvider, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, OIDCProvider: "azure"})
	if err != nil {
		t.Fatalf("List(oidcProvider=azure): %v", err)
	}
	if len(byProvider.Data) != 1 || byProvider.Data[0].ID == rbac.DefaultSystemUserID {
		t.Errorf("List(oidcProvider=azure) = %+v, want just Alice (no built-in default)", byProvider.Data)
	}
}

func TestUsersService_ListSearchMatchesOrExcludesDefault(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newUsersTestService(t)
	ctx := t.Context()

	matched, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, Search: "default"})
	if err != nil {
		t.Fatalf("List(search=default): %v", err)
	}
	if len(matched.Data) != 1 || matched.Data[0].ID != rbac.DefaultSystemUserID {
		t.Errorf("List(search=default) = %+v, want just the built-in default", matched.Data)
	}

	unmatched, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, Search: "nobody-named-this"})
	if err != nil {
		t.Fatalf("List(search=nobody-named-this): %v", err)
	}
	if len(unmatched.Data) != 0 {
		t.Errorf("List(search=nobody-named-this) = %+v, want no matches", unmatched.Data)
	}
}

func TestUsersService_ListPaginationAcrossDefaultBoundary(t *testing.T) {
	t.Parallel()
	svc, usersRepo, _, _ := newUsersTestService(t)
	ctx := t.Context()
	alice := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "", nil)
	bob := seedUserRow(ctx, t, usersRepo, "s2", "Bob", "bob@example.com", "azure", "", nil)
	carol := seedUserRow(ctx, t, usersRepo, "s3", "Carol", "carol@example.com", "azure", "", nil)

	page1, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 3, SortBy: "displayName", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	page2, err := svc.List(ctx, UserListQuery{PageNumber: 2, PageSize: 3, SortBy: "displayName", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}

	if want := 3 + len(listedSystemUsers()); page1.Meta.Pagination.Count != want || page2.Meta.Pagination.Count != want {
		t.Fatalf("page1/page2 count = %d/%d, want %d (3 seeded here + the listed system users)", page1.Meta.Pagination.Count, page2.Meta.Pagination.Count, want)
	}
	var page1IDs, page2IDs []string
	for _, u := range page1.Data {
		page1IDs = append(page1IDs, u.ID)
	}
	for _, u := range page2.Data {
		page2IDs = append(page2IDs, u.ID)
	}
	if !slices.Equal(page1IDs, []string{alice, bob, carol}) {
		t.Errorf("page1 ids = %v, want [alice, bob, carol] (Default/Eval/Evidence sort after Carol)", page1IDs)
	}
	if want := []string{rbac.DefaultSystemUserID, rbac.EvalSystemUserID, rbac.EvidenceSystemUserID}; !slices.Equal(page2IDs, want) {
		t.Errorf("page2 ids = %v, want %v — no row dropped or duplicated across the boundary", page2IDs, want)
	}
}

func TestUsersService_RoleIDFilterMatchesEmbeddedDefaultRole(t *testing.T) {
	t.Parallel()
	svc, usersRepo, _, composer := newUsersTestService(t)
	ctx := t.Context()

	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "", nil)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: rbac.RoleSystemInvokeAll})
	if err != nil {
		t.Fatalf("List(roleId=%s): %v", rbac.RoleSystemInvokeAll, err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != rbac.DefaultSystemUserID {
		t.Errorf("List(roleId=%s) = %+v, want just the seeded default", rbac.RoleSystemInvokeAll, page.Data)
	}
}

func seedDefaultSystemUserShadowRow(ctx context.Context, t *testing.T, usersRepo *store.UsersRepo) *store.User {
	t.Helper()
	if err := usersRepo.DeleteSystemUser(ctx, rbac.DefaultSystemUserID); err != nil {
		t.Fatalf("remove the seeded default row: %v", err)
	}
	shadow := &store.User{
		Issuer: rbac.SystemUserIssuer, Sub: rbac.DefaultSystemUserSub,
		DisplayName: usPtr("Migrated Default"), ProvisionedVia: rbac.ProvisionedViaSystem,
		CreatedBy: "x", UpdatedBy: "x",
	}
	if err := usersRepo.UpsertUser(ctx, shadow, nil); err != nil {
		t.Fatalf("seed shadow row: %v", err)
	}
	if shadow.ID == rbac.DefaultSystemUserID {
		t.Fatal("shadow row got the seed id; the test needs a distinct one")
	}
	return shadow
}

func TestUsersService_ShadowRowForDefaultSystemUserIsNotDoubleListed(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	shadow := seedDefaultSystemUserShadowRow(ctx, t, usersRepo)
	customRole := seedRole(ctx, t, rbacRepo, composer, "custom-grant")
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{shadow.ID: customRole})

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var matches []UserResponse
	for _, u := range page.Data {
		if u.Sub == rbac.DefaultSystemUserSub {
			matches = append(matches, u)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("got %d rows for sub %q, want exactly 1 (no double-listing): %+v", len(matches), rbac.DefaultSystemUserSub, matches)
	}
	if matches[0].ID != shadow.ID {
		t.Errorf("listed id = %s, want the shadow row's own id %s", matches[0].ID, shadow.ID)
	}
	if got := roleIDsOf(matches[0].Roles); !slices.Contains(got, customRole) || !slices.Contains(got, rbac.RoleSystemInvokeAll) {
		t.Errorf("roles = %v, want the DB grant %q and the embedded %q together", got, customRole, rbac.RoleSystemInvokeAll)
	}
}

func TestUsersService_RoleIDFilterDoesNotDoubleListShadowRow(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	shadow := seedDefaultSystemUserShadowRow(ctx, t, usersRepo)
	role := seedRole(ctx, t, rbacRepo, composer, "custom-grant")
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{shadow.ID: role})

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role})
	if err != nil {
		t.Fatalf("List(roleId=%s): %v", role, err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != shadow.ID {
		t.Errorf("List(roleId=%s) = %+v, want exactly the shadow row once", role, page.Data)
	}
}

func TestCompareUsersForSort_NeverLoggedInSortsAsMaxValue(t *testing.T) {
	loggedInAt := int64(1000)
	neverLoggedIn := store.User{ID: "a"}
	loggedIn := store.User{ID: "b", LastLoginAt: &loggedInAt}

	if got := compareUsersForSort(neverLoggedIn, loggedIn, "last_login_at", "ASC"); got <= 0 {
		t.Errorf("ASC: never-logged-in user compared %d, want > 0 (sorts last, matching Postgres NULLS LAST)", got)
	}
	if got := compareUsersForSort(neverLoggedIn, loggedIn, "last_login_at", "DESC"); got >= 0 {
		t.Errorf("DESC: never-logged-in user compared %d, want < 0 (sorts first, matching Postgres NULLS FIRST)", got)
	}
}

func TestCompareUsersForSort_AllColumnsDiscriminate(t *testing.T) {
	earlier, later := int64(1000), int64(2000)
	a := store.User{ID: "a", DisplayName: usPtr("Alice"), Email: usPtr("alice@example.com"), LastLoginAt: &earlier, CreatedTime: 1000, UpdatedTime: 1000}
	b := store.User{ID: "b", DisplayName: usPtr("Bob"), Email: usPtr("bob@example.com"), LastLoginAt: &later, CreatedTime: 2000, UpdatedTime: 2000}

	for _, column := range []string{"display_name", "email", "last_login_at", "created_time", "updated_time"} {
		for _, order := range []string{"ASC", "DESC"} {
			t.Run(column+"_"+order, func(t *testing.T) {
				got := compareUsersForSort(a, b, column, order)
				if got == 0 {
					t.Fatalf("compareUsersForSort(a, b, %q, %q) = 0, want a nonzero discriminator", column, order)
				}
				wantNegative := order == "ASC"
				if gotNegative := got < 0; gotNegative != wantNegative {
					t.Errorf("compareUsersForSort(a, b, %q, %q) = %d, want negative=%v", column, order, got, wantNegative)
				}
			})
		}
	}
}

func TestUsersService_GetByIDNotFound(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newUsersTestService(t)
	_, err := svc.GetByID(t.Context(), "00000000-0000-0000-0000-000000000000")
	if _, ok := errors.AsType[*NotFoundError](err); !ok {
		t.Fatalf("GetByID(missing) err = %v, want NotFoundError", err)
	}
}

func TestUsersService_ResolvesDirectGrantForEmaillessUser(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleID := seedRole(ctx, t, rbacRepo, composer, "sub-keyed")
	u := &store.User{Issuer: "iss", Sub: "no-email-sub", OIDCProvider: "azure", CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := rbacRepo.AddUserRole(ctx, u.ID, roleID, "x"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := svc.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if ids := roleIDsOf(got.Roles); len(ids) != 1 || ids[0] != roleID {
		t.Errorf("roles = %v, want a single direct grant [%s] (email is nil)", got.Roles, roleID)
	}
}

func TestUsersService_SameRoleShowsPerSource(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleID := seedRole(ctx, t, rbacRepo, composer, "shared")
	u := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", `{"groups":["eng"]}`, []string{"eng"})
	if err := rbacRepo.AddUserRole(ctx, u, roleID, "x"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "eng", Name: "Engineers", CreatedBy: "x", UpdatedBy: "x"}, []string{roleID}); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	got, err := svc.GetByID(ctx, u)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	sources := map[string]bool{}
	for _, r := range got.Roles {
		if r.RoleID != roleID {
			t.Errorf("unexpected role %q in %v", r.RoleID, got.Roles)
		}
		sources[r.Source] = true
	}
	if len(got.Roles) != 2 || !sources[rbac.SourceDirect] || !sources[rbac.SourceClaim] {
		t.Errorf("roles = %v, want the same role once per source (direct + claim)", got.Roles)
	}
}

func fakeAgentScope(knownID, scope string) InvokeScopeResolver {
	return func(_ context.Context, id string) (string, error) {
		if id == knownID {
			return scope, nil
		}
		return "", &NotFoundError{Resource: "agent", ID: id}
	}
}

func TestUsersService_AgentInvokeFilter(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("known", rbac.AgentInvokeScope("known")), nil)

	direct := seedRoleWithScopes(ctx, t, rbacRepo, composer, "invoke-known", []string{rbac.AgentInvokeScope("known")})
	wild := seedRoleWithScopes(ctx, t, rbacRepo, composer, "invoke-any-agent", []string{rbac.ScopeAgentInvokeAny})
	other := seedRoleWithScopes(ctx, t, rbacRepo, composer, "invoke-other", []string{rbac.AgentInvokeScope("other")})

	uDirect := seedUserRow(ctx, t, usersRepo, "s1", "Direct", "d@example.com", "azure", "", nil)
	uWild := seedUserRow(ctx, t, usersRepo, "s2", "Wild", "w@example.com", "azure", "", nil)
	uOther := seedUserRow(ctx, t, usersRepo, "s3", "Other", "o@example.com", "azure", "", nil)
	_ = seedUserRow(ctx, t, usersRepo, "s4", "None", "n@example.com", "azure", "", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{uDirect: direct, uWild: wild, uOther: other})

	page, err := svc.ListAgentAccess(ctx, "known", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("filter agentId=known: %v", err)
	}
	want := []string{uDirect, uWild}
	slices.Sort(want)
	if got := accessUserIDs(page); !slices.Equal(got, want) {
		t.Errorf("agentId=known users = %v, want %v (direct + agent:*:invoke)", got, want)
	}

	if _, err := svc.ListAgentAccess(ctx, "missing", ComponentAccessQuery{PageNumber: 1, PageSize: 20}); !isNotFound(err) {
		t.Fatalf("agentId=missing err = %v, want NotFoundError (404)", err)
	}
}

func TestUsersService_InvokeScopeCategorySplit(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(
		fakeAgentScope("a1", rbac.AgentInvokeScope("a1")),
		func(_ context.Context, id string) (string, error) { return rbac.WorkflowInvokeScope(id), nil },
	)

	agentRole := seedRoleWithScopes(ctx, t, rbacRepo, composer, "any-agent", []string{rbac.ScopeAgentInvokeAny})
	wfRole := seedRoleWithScopes(ctx, t, rbacRepo, composer, "any-workflow", []string{rbac.ScopeWorkflowInvokeAny})
	superRole := seedRoleWithScopes(ctx, t, rbacRepo, composer, "super", []string{"*:*:*"})

	uAgent := seedUserRow(ctx, t, usersRepo, "s1", "AgentWild", "a@example.com", "azure", "", nil)
	uWf := seedUserRow(ctx, t, usersRepo, "s2", "WfWild", "wf@example.com", "azure", "", nil)
	uSuper := seedUserRow(ctx, t, usersRepo, "s3", "Super", "su@example.com", "azure", "", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{uAgent: agentRole, uWf: wfRole, uSuper: superRole})

	agentPage, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("filter agentId: %v", err)
	}
	wantAgent := []string{uAgent, uSuper}
	slices.Sort(wantAgent)
	if got := accessUserIDs(agentPage); !slices.Equal(got, wantAgent) {
		t.Errorf("agentId users = %v, want %v (agent:*:invoke + superadmin, NOT workflow:*:invoke)", got, wantAgent)
	}

	wfPage, err := svc.ListWorkflowAccess(ctx, "w1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("filter workflowId: %v", err)
	}
	wantWf := []string{uSuper, uWf}
	slices.Sort(wantWf)
	if got := accessUserIDs(wfPage); !slices.Equal(got, wantWf) {
		t.Errorf("workflowId users = %v, want %v (workflow:*:invoke + superadmin, NOT agent:*:invoke)", got, wantWf)
	}
}

func TestUsersService_AgentInvokeViaClaimMapping(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)

	role := seedRoleWithScopes(ctx, t, rbacRepo, composer, "invoker", []string{rbac.AgentInvokeScope("a1")})
	u := seedUserRow(ctx, t, usersRepo, "s1", "Claimed", "c@example.com", "azure", `{"department":"finance"}`, nil)
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{
		OIDCProvider: "azure", ClaimKey: "department", ClaimValue: "finance",
		Name: "Finance", CreatedBy: "x", UpdatedBy: "x",
	}, []string{role}); err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("filter agentId: %v", err)
	}
	if got := accessUserIDs(page); len(got) != 1 || got[0] != u {
		t.Errorf("agentId=a1 users = %v, want [%s] (claim-mapped invoke scope)", got, u)
	}
}

func TestUsersService_AgentInvokeViaDefaultRole(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)

	role := seedRoleWithScopes(ctx, t, rbacRepo, composer, "default-invoker", []string{rbac.AgentInvokeScope("a1")})
	if err := rbacRepo.SetDefaultRoles(ctx, []string{role}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	u := seedUserRow(ctx, t, usersRepo, "s1", "NoGrants", "n@example.com", "azure", "", nil)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("filter agentId: %v", err)
	}
	if got := accessUserIDs(page); len(got) != 1 || got[0] != u {
		t.Errorf("agentId=a1 users = %v, want [%s] (invoke scope inherited from the default role)", got, u)
	}
}

func TestUsersService_InvokeFilterResolverNotConfigured(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newUsersTestService(t)
	if _, err := svc.ListAgentAccess(t.Context(), "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20}); err == nil {
		t.Fatal("List with agentId and no resolver wired should error, got nil")
	}
}

func TestUsersService_InvokeFilterComposerNotConfigured(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewUsersService(store.NewUsersRepo(db), store.NewRBACRepo(db), slog.Default())
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)
	if _, err := svc.ListAgentAccess(t.Context(), "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20}); err == nil {
		t.Fatal("List with agentId and nil composer should error, got nil")
	}
}

func isNotFound(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

func TestUsersService_ComponentAccessListsSeededSystemUsers(t *testing.T) {
	t.Parallel()
	svc, _, _, composer := newUsersTestService(t)
	ctx := t.Context()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	svc.SetInvokeScopeResolvers(func(_ context.Context, id string) (string, error) {
		return rbac.AgentInvokeScope(id), nil
	}, nil)

	systemIDs := func(agentID string) []string {
		t.Helper()
		page, err := svc.ListAgentAccess(ctx, agentID, ComponentAccessQuery{PageNumber: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("ListAgentAccess(%s): %v", agentID, err)
		}
		var ids []string
		for _, u := range page.Data {
			if !u.IsSystemUser {
				t.Errorf("%s: %+v not marked as a system user", agentID, u)
			}
			ids = append(ids, u.ID)
		}
		slices.Sort(ids)
		return ids
	}

	anyAgent := []string{rbac.DefaultSystemUserID, rbac.EvalSystemUserID}
	slices.Sort(anyAgent)
	if got := systemIDs("Research"); !slices.Equal(got, anyAgent) {
		t.Errorf("Research access = %v, want Default and Eval %v", got, anyAgent)
	}
	if got := systemIDs("Orchestrator"); !slices.Equal(got, anyAgent) {
		t.Errorf("Orchestrator access = %v, want only Default and Eval %v", got, anyAgent)
	}
}

func TestUsersService_AgentAccessBeyondInClauseCap(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)

	role := seedRoleWithScopes(ctx, t, rbacRepo, composer, "default-invoker", []string{rbac.ScopeAgentInvokeAny})
	if err := rbacRepo.SetDefaultRoles(ctx, []string{role}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	const population = dbutil.InClauseCap + 1
	for i := range population {
		seedUserRow(ctx, t, usersRepo, fmt.Sprintf("s%d", i), fmt.Sprintf("User %04d", i), fmt.Sprintf("u%04d@example.com", i), "azure", "", nil)
	}
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	page, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListAgentAccess with %d matching users: %v", population, err)
	}
	if want := population + invokeAnySystemUsers; page.Meta.Pagination.Count != want {
		t.Errorf("count = %d, want %d (every user inherits agent:*:invoke, plus the invoke-any system users)", page.Meta.Pagination.Count, want)
	}
	if len(page.Data) != 20 {
		t.Errorf("page size = %d, want 20", len(page.Data))
	}
}

func TestUsersService_AgentAccessUnresolvableComposerErrors(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, _ := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)

	role := &store.RBACRole{Name: "superadmin", CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, role, []string{"*:*:*"}, nil); err != nil {
		t.Fatalf("seed superadmin: %v", err)
	}
	if err := rbacRepo.SetDefaultRoles(ctx, []string{role.ID}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "a@example.com", "azure", "", nil)
	seedUserRow(ctx, t, usersRepo, "s2", "Bob", "b@example.com", "azure", "", nil)

	page, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err == nil {
		t.Fatalf("want an error, got a %d-row page (count %d) — an unrebuilt composer must not read as 'nobody has access'",
			len(page.Data), page.Meta.Pagination.Count)
	}
	if isNotFound(err) {
		t.Errorf("err = %v, want a server-side failure rather than a 404", err)
	}
}

func TestUsersService_RoleIDFilterComposerNotConfigured(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	rbacRepo := store.NewRBACRepo(db)
	logger, logs := captureLogger()
	svc := NewUsersService(usersRepo, rbacRepo, logger)
	ctx := t.Context()
	role := &store.RBACRole{Name: "some-role", CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "a@example.com", "azure", "", nil)

	if _, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role.ID}); err == nil {
		t.Fatal("List with roleId and nil composer should error, got nil")
	}
	if !strings.Contains(logs.String(), "composer not configured") {
		t.Errorf("log = %q, want a line naming the nil-composer cause", logs.String())
	}
}

func TestUsersService_RoleIDFilterUnresolvableComposerErrors(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	rbacRepo := store.NewRBACRepo(db)
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), slog.Default())
	logger, logs := captureLogger()
	svc := NewUsersService(usersRepo, rbacRepo, logger)
	svc.SetGrantRefresh(composer, nil, nil)
	ctx := t.Context()

	role := &store.RBACRole{Name: "superadmin", CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, role, []string{"*:*:*"}, nil); err != nil {
		t.Fatalf("seed superadmin: %v", err)
	}
	if err := rbacRepo.SetDefaultRoles(ctx, []string{role.ID}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	seedUserRow(ctx, t, usersRepo, "s1", "Alice", "a@example.com", "azure", "", nil)
	seedUserRow(ctx, t, usersRepo, "s2", "Bob", "b@example.com", "azure", "", nil)

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role.ID})
	if err == nil {
		t.Fatalf("want an error, got a %d-row page (count %d) — an unrebuilt composer must not read as 'nobody holds this role'",
			len(page.Data), page.Meta.Pagination.Count)
	}
	if isNotFound(err) {
		t.Errorf("err = %v, want a server-side failure rather than a 404", err)
	}
	if !strings.Contains(logs.String(), "not initialized") {
		t.Errorf("log = %q, want the underlying \"not initialized\" composer error", logs.String())
	}
}

func TestUsersService_AgentAccessUnparseableClaimsError(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)

	role := seedRoleWithScopes(ctx, t, rbacRepo, composer, "invoker", []string{rbac.AgentInvokeScope("a1")})
	u := seedUserRow(ctx, t, usersRepo, "s1", "Corrupt", "c@example.com", "azure", "{not json", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{u: role})

	_, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20})
	if err == nil {
		t.Fatal("want an error for unparseable stored claims, got nil")
	}
	if !errors.Is(err, errClaimsNotJSON) {
		t.Errorf("err = %v, want it to wrap errClaimsNotJSON", err)
	}
	if strings.Contains(err.Error(), "invalid character") {
		t.Errorf("err = %v, want no json parser detail (it quotes a fragment of the claims blob)", err)
	}
}

func TestUsersService_AgentAccessSearchAndPaging(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	svc.SetInvokeScopeResolvers(fakeAgentScope("a1", rbac.AgentInvokeScope("a1")), nil)

	role := seedRoleWithScopes(ctx, t, rbacRepo, composer, "invoker", []string{rbac.AgentInvokeScope("a1")})
	alice := seedUserRow(ctx, t, usersRepo, "s1", "Alice Ant", "alice@example.com", "azure", "", nil)
	bob := seedUserRow(ctx, t, usersRepo, "s2", "Bob Bee", "bob@example.com", "azure", "", nil)
	carol := seedUserRow(ctx, t, usersRepo, "s3", "Carol Cat", "carol@example.com", "azure", "", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{alice: role, bob: role, carol: role})

	page, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if want := 3 + invokeAnySystemUsers; page.Meta.Pagination.Count != want || len(page.Data) != 2 {
		t.Errorf("page 1 = %d rows of count %d, want 2 of %d", len(page.Data), page.Meta.Pagination.Count, want)
	}
	if page.Data[0].DisplayName != "Alice Ant" || page.Data[1].DisplayName != "Bob Bee" {
		t.Errorf("page 1 order = %q, %q; want label order Alice Ant, Bob Bee", page.Data[0].DisplayName, page.Data[1].DisplayName)
	}

	page2, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Data) != 2 || page2.Data[0].ID != carol || page2.Data[1].ID != rbac.DefaultSystemUserID {
		t.Errorf("page 2 = %v, want Carol then the seeded Default", page2.Data)
	}

	found, err := svc.ListAgentAccess(ctx, "a1", ComponentAccessQuery{PageNumber: 1, PageSize: 20, Search: "BOB@ex"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found.Data) != 1 || found.Data[0].ID != bob {
		t.Errorf("search 'BOB@ex' = %v, want just Bob (case-insensitive, email included)", found.Data)
	}
	if found.Meta.Pagination.Count != 1 {
		t.Errorf("search count = %d, want 1 (search-filtered total)", found.Meta.Pagination.Count)
	}

	raw, err := json.Marshal(found.Data[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"displayName", "email", "id", "isSystemUser", "userName"}
	got := make([]string, 0, len(fields))
	for k := range fields {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("access DTO fields = %v, want exactly %v (no roles/groups/sub/issuer/audit fields)", got, want)
	}
}

func TestUsersService_UnparseableClaimsKeepsDirectGrants(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	role := seedRoleWithScopes(ctx, t, rbacRepo, composer, "direct-role", []string{rbac.AgentInvokeScope("a1")})
	u := seedUserRow(ctx, t, usersRepo, "s1", "Corrupt", "c@example.com", "azure", "{not json", nil)
	grantRoles(ctx, t, rbacRepo, composer, map[string]string{u: role})

	got, err := svc.GetByID(ctx, u)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !slices.Contains(roleIDsOf(got.Roles), role) {
		t.Errorf("roles = %v, want the direct grant %s despite unparseable claims", got.Roles, role)
	}

	page, err := svc.List(ctx, UserListQuery{PageNumber: 1, PageSize: 20, RoleID: role})
	if err != nil {
		t.Fatalf("filter roleId: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != u {
		t.Errorf("roleId=%s users = %v, want the corrupt-claims user (audit filter must not under-report)", role, page.Data)
	}
}
