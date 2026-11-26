// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"testing"
)

func TestRBACRepo_ListRolesPage(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := t.Context()
	for _, name := range []string{"page-b", "Page-A", "page-c"} {
		if err := repo.CreateRole(ctx, &RBACRole{Name: name, Description: "paged " + name}, []string{"agent:*:invoke"}, nil); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	custom := false
	names := func(roles []*RBACRole) []string {
		out := make([]string, len(roles))
		for i, r := range roles {
			out[i] = r.Name
		}
		return out
	}

	roles, total, err := repo.ListRolesPage(ctx, RoleListOptions{Builtin: &custom, PageQuery: PageQuery{Offset: 1, Limit: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"page-b", "page-c"}; total != 3 || !slices.Equal(names(roles), want) {
		t.Errorf("offset 1 limit 2 = %v of %d, want %v of 3 (name order ignores case)", names(roles), total, want)
	}

	roles, total, err = repo.ListRolesPage(ctx, RoleListOptions{Builtin: &custom, PageQuery: PageQuery{Sort: "name", Desc: true, Limit: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"page-c"}; total != 3 || !slices.Equal(names(roles), want) {
		t.Errorf("name desc limit 1 = %v of %d, want %v of 3", names(roles), total, want)
	}

	roles, total, err = repo.ListRolesPage(ctx, RoleListOptions{Builtin: &custom, Search: "PAGED PAGE-B", PageQuery: PageQuery{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"page-b"}; total != 1 || !slices.Equal(names(roles), want) {
		t.Errorf("search = %v of %d, want %v of 1", names(roles), total, want)
	}

	roles, total, err = repo.ListRolesPage(ctx, RoleListOptions{Builtin: &custom, PageQuery: PageQuery{Limit: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(roles) != 0 {
		t.Errorf("limit 0 = %d rows of %d, want 0 of 3", len(roles), total)
	}

	if _, _, sortErr := repo.ListRolesPage(ctx, RoleListOptions{PageQuery: PageQuery{Sort: "name; DROP TABLE rbac_roles", Limit: 1}}); sortErr == nil {
		t.Error("a sort column outside the whitelist was accepted")
	}

	builtin := true
	roles, _, err = repo.ListRolesPage(ctx, RoleListOptions{Builtin: &builtin, PageQuery: PageQuery{Limit: 100}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		if !r.Builtin {
			t.Errorf("builtin filter returned custom role %s", r.Name)
		}
	}
}

func TestRBACRepo_RoleEdges(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := t.Context()
	parent := &RBACRole{Name: "edge-parent"}
	if err := repo.CreateRole(ctx, parent, []string{"agent:*:invoke"}, nil); err != nil {
		t.Fatal(err)
	}
	child := &RBACRole{Name: "edge-child"}
	if err := repo.CreateRole(ctx, child, []string{"workflow:*:invoke", "agent_builder:*:read"}, []string{parent.ID}); err != nil {
		t.Fatal(err)
	}

	scopes, parents, err := repo.RoleEdges(ctx, []string{parent.ID, child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := scopes[child.ID], []string{"agent_builder:*:read", "workflow:*:invoke"}; !slices.Equal(got, want) {
		t.Errorf("child scopes = %v, want %v", got, want)
	}
	if got, want := parents[child.ID], []string{parent.ID}; !slices.Equal(got, want) {
		t.Errorf("child parents = %v, want %v", got, want)
	}
	if len(parents[parent.ID]) != 0 {
		t.Errorf("parent has parents %v", parents[parent.ID])
	}
}

func TestRBACRepo_ListGrantsPage(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	users := NewUsersRepo(db)
	ctx := t.Context()
	role := &RBACRole{Name: "grant-page-role"}
	if err := repo.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatal(err)
	}
	other := &RBACRole{Name: "grant-page-other"}
	if err := repo.CreateRole(ctx, other, nil, nil); err != nil {
		t.Fatal(err)
	}
	u := &User{Issuer: "https://idp.example.com", Sub: "grant-page-user", ProvisionedVia: "jit"}
	if err := users.UpsertUser(ctx, u, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddUserRole(ctx, u.ID, role.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		if err := repo.CreatePendingUserRole(ctx, &RBACPendingUserRole{SubjectType: PendingSubjectEmail, SubjectValue: email, RoleID: role.ID, CreatedBy: "tester", UpdatedBy: "tester"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreatePendingUserRole(ctx, &RBACPendingUserRole{SubjectType: PendingSubjectEmail, SubjectValue: "c@example.com", RoleID: other.ID, CreatedBy: "tester", UpdatedBy: "tester"}); err != nil {
		t.Fatal(err)
	}

	grants, total, err := repo.ListGrantsPage(ctx, GrantListOptions{RoleID: role.ID, PageQuery: PageQuery{Sort: "state", Limit: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(grants) != 2 || grants[0].State != "concrete" || grants[0].UserID != u.ID || grants[1].State != "pending" || grants[1].UserID != "" {
		t.Fatalf("role page = %+v of %d, want the concrete grant then a pending one, of 3", grants, total)
	}
	if grants[0].CreatedBy != "tester" || grants[0].CreatedTime == 0 {
		t.Errorf("concrete grant lost its audit fields: %+v", grants[0])
	}

	_, total, err = repo.ListGrantsPage(ctx, GrantListOptions{State: "pending", PageQuery: PageQuery{Limit: 0}})
	if err != nil || total != 3 {
		t.Errorf("pending count = %d (%v), want 3", total, err)
	}

	got, err := repo.GetGrant(ctx, grants[1].ID)
	if err != nil || got == nil || got.State != "pending" || got.SubjectValue == "" {
		t.Errorf("GetGrant(pending) = %+v, %v", got, err)
	}
	if got, err := repo.GetGrant(ctx, "0190a5f0-0000-7000-8000-000000000000"); err != nil || got != nil {
		t.Errorf("GetGrant(missing) = %+v, %v, want nil", got, err)
	}
}

func TestRBACRepo_ListClaimMappingsPage(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := t.Context()
	role := &RBACRole{Name: "mapping-page-role"}
	if err := repo.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatal(err)
	}
	other := &RBACRole{Name: "mapping-page-other"}
	if err := repo.CreateRole(ctx, other, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		name, provider, value string
		roles                 []string
	}{
		{"Engineering", "okta", "engineering", []string{role.ID}},
		{"Support", "okta", "support", []string{role.ID, other.ID}},
		{"Sales", "azure", "sales", []string{other.ID}},
	} {
		if err := repo.CreateClaimMapping(ctx, &RBACIdpClaimMapping{Name: m.name, OIDCProvider: m.provider, ClaimKey: "groups", ClaimValue: m.value}, m.roles); err != nil {
			t.Fatal(err)
		}
	}

	mappings, roles, total, err := repo.ListClaimMappingsPage(ctx, ClaimMappingListOptions{RoleID: role.ID, PageQuery: PageQuery{Sort: "name", Desc: true, Limit: 1, Offset: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(mappings) != 1 || mappings[0].Name != "Engineering" {
		t.Fatalf("role filter page 2 = %+v of %d, want Engineering of 2", mappings, total)
	}
	if got := roles[mappings[0].ID]; !slices.Equal(got, []string{role.ID}) {
		t.Errorf("Engineering roles = %v", got)
	}

	mappings, _, total, err = repo.ListClaimMappingsPage(ctx, ClaimMappingListOptions{OIDCProvider: "okta", Search: "SUPP", PageQuery: PageQuery{Limit: 10}})
	if err != nil || total != 1 || len(mappings) != 1 || mappings[0].Name != "Support" {
		t.Errorf("provider + search = %+v of %d (%v), want Support", mappings, total, err)
	}
}

func TestUsersRepo_ListSystemUsersPage(t *testing.T) {
	db := TestDB(t)
	users := NewUsersRepo(db)
	ctx := t.Context()
	for _, name := range []string{"page-bot-b", "page-bot-a"} {
		if err := users.UpsertUser(ctx, &User{Issuer: "sam:system", Sub: "system:" + name, ProvisionedVia: "system"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := users.UpsertUser(ctx, &User{Issuer: "https://idp.example.com", Sub: "page-bot-human", ProvisionedVia: "jit"}, nil); err != nil {
		t.Fatal(err)
	}

	got, total, err := users.ListSystemUsersPage(ctx, SystemUserListOptions{Search: "PAGE-BOT", PageQuery: PageQuery{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	subs := make([]string, len(got))
	for i, u := range got {
		subs[i] = u.Sub
	}
	if want := []string{"system:page-bot-a", "system:page-bot-b"}; total != 2 || !slices.Equal(subs, want) {
		t.Errorf("search = %v of %d, want %v (system users only, by name)", subs, total, want)
	}

	_, total, err = users.ListSystemUsersPage(ctx, SystemUserListOptions{Search: "page-bot", ExcludeSubs: []string{"system:page-bot-a"}, PageQuery: PageQuery{Limit: 10}})
	if err != nil || total != 1 {
		t.Errorf("excluded count = %d (%v), want 1", total, err)
	}
}

func TestUserIdentityClaimsRepo_ClaimsForUserPage(t *testing.T) {
	db := TestDB(t)
	users := NewUsersRepo(db)
	claims := NewUserIdentityClaimsRepo(db)
	ctx := t.Context()
	u := &User{Issuer: "https://idp.example.com", Sub: "claims-page", ProvisionedVia: "jit"}
	other := &User{Issuer: "https://idp.example.com", Sub: "claims-page-other", ProvisionedVia: "jit"}
	write := IdentityClaimWrite{Replace: true, Claims: []UserIdentityClaim{
		{ClaimKey: "phone_number", ClaimValueCanonical: "15550000002", Verified: true},
		{ClaimKey: "email", ClaimValueCanonical: "a@example.com", Verified: true},
		{ClaimKey: "phone_number", ClaimValueCanonical: "15550000001"},
	}}
	for _, owner := range []*User{u, other} {
		if err := users.UpsertUserWithClaims(ctx, owner, nil, write); err != nil {
			t.Fatal(err)
		}
	}

	var seen []string
	for offset := 0; offset < 4; offset += 2 {
		got, total, err := claims.ClaimsForUserPage(ctx, u.ID, offset, 2)
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 {
			t.Errorf("offset %d: total = %d, want 3 (this user's claims only)", offset, total)
		}
		for _, c := range got {
			seen = append(seen, c.ClaimKey+"="+c.ClaimValueCanonical)
		}
	}
	if want := []string{"email=a@example.com", "phone_number=15550000001", "phone_number=15550000002"}; !slices.Equal(seen, want) {
		t.Errorf("pages = %v, want %v (by key then value, each claim once)", seen, want)
	}
}
