// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestRBACRepo_UpdateDeleteRole_NonUUID(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	role := &RBACRole{ID: "not-a-uuid", Name: "x", CreatedBy: "system", UpdatedBy: "system"}
	if err := repo.UpdateRole(ctx, role, nil, nil); !errors.Is(err, ErrRBACNotFound) {
		t.Errorf("UpdateRole(non-uuid) err = %v, want ErrRBACNotFound", err)
	}
	if err := repo.DeleteRole(ctx, "not-a-uuid"); !errors.Is(err, ErrRBACNotFound) {
		t.Errorf("DeleteRole(non-uuid) err = %v, want ErrRBACNotFound", err)
	}
}

func TestRBACRepo_RoleCRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	role := &RBACRole{
		Name:        "sam_admin",
		Description: "Full administrative access",
		CreatedBy:   "system",
		UpdatedBy:   "system",
	}
	scopes := []string{"*", "rbac:_:read"}
	if err := repo.CreateRole(ctx, role, scopes, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if role.ID == "" || role.CreatedTime == 0 {
		t.Fatal("expected ID and CreatedTime to be populated")
	}

	got, gotScopes, gotParents, err := repo.GetRoleByID(ctx, role.ID)
	if err != nil {
		t.Fatalf("get role: %v", err)
	}
	if got == nil || got.Name != "sam_admin" {
		t.Fatalf("unexpected role: %+v", got)
	}
	slices.Sort(gotScopes)
	if !slices.Equal(gotScopes, []string{"*", "rbac:_:read"}) {
		t.Errorf("scopes = %v, want [* rbac:_:read]", gotScopes)
	}
	if len(gotParents) != 0 {
		t.Errorf("expected no parents, got %v", gotParents)
	}

	byName, err := repo.GetRoleByName(ctx, "sam_admin")
	if err != nil {
		t.Fatalf("get role by name: %v", err)
	}
	if byName == nil || byName.ID != role.ID {
		t.Fatal("expected GetRoleByName to find role")
	}

	missing, err := repo.GetRoleByName(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Fatal("expected nil for missing name")
	}

	dup := &RBACRole{
		Name:      "sam_admin",
		CreatedBy: "system",
		UpdatedBy: "system",
	}
	if e := repo.CreateRole(ctx, dup, nil, nil); e == nil {
		t.Fatal("expected UNIQUE name violation")
	}

	got.Description = "Updated description"
	got.UpdatedBy = "alice"
	if e := repo.UpdateRole(ctx, got, []string{"rbac:_:read"}, nil); e != nil {
		t.Fatalf("update role: %v", e)
	}

	updated, updatedScopes, _, err := repo.GetRoleByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if updated.Description != "Updated description" {
		t.Errorf("description = %q, want Updated description", updated.Description)
	}
	if !slices.Equal(updatedScopes, []string{"rbac:_:read"}) {
		t.Errorf("scopes after update = %v, want [rbac:_:read]", updatedScopes)
	}

	if e := repo.DeleteRole(ctx, got.ID); e != nil {
		t.Fatalf("delete role: %v", e)
	}
	gone, _, _, err := repo.GetRoleByID(ctx, got.ID)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if gone != nil {
		t.Fatal("expected nil after delete")
	}

	if err := repo.DeleteRole(ctx, got.ID); err == nil {
		t.Fatal("expected delete-not-found error")
	}
}

func TestRBACRepo_BuiltinFlagRoundTrips(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	flagged := &RBACRole{Name: "rt_builtin", CreatedBy: "system", UpdatedBy: "system"}
	if err := repo.CreateRole(ctx, flagged, []string{"rbac:_:read"}, nil); err != nil {
		t.Fatalf("create flagged role: %v", err)
	}
	plain := &RBACRole{Name: "rt_plain", CreatedBy: "system", UpdatedBy: "system"}
	if err := repo.CreateRole(ctx, plain, []string{"rbac:_:read"}, nil); err != nil {
		t.Fatalf("create plain role: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE rbac_roles SET builtin = 1 WHERE id = ?`, flagged.ID); err != nil {
		t.Fatalf("flag builtin: %v", err)
	}

	byID, _, _, err := repo.GetRoleByID(ctx, flagged.ID)
	if err != nil {
		t.Fatalf("GetRoleByID: %v", err)
	}
	if byID == nil || !byID.Builtin {
		t.Errorf("GetRoleByID builtin = %v, want true", byID)
	}
	byName, err := repo.GetRoleByName(ctx, "rt_builtin")
	if err != nil {
		t.Fatalf("GetRoleByName: %v", err)
	}
	if byName == nil || !byName.Builtin {
		t.Errorf("GetRoleByName builtin = %v, want true", byName)
	}

	list, err := repo.ListRoles(ctx)
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, roles := range [][]*RBACRole{list, snap.Roles} {
		flags := map[string]bool{}
		for _, r := range roles {
			flags[r.Name] = r.Builtin
		}
		if !flags["rt_builtin"] {
			t.Errorf("rt_builtin builtin = false, want true")
		}
		if flags["rt_plain"] {
			t.Errorf("rt_plain builtin = true, want false")
		}
	}
}

func TestRBACRepo_BuiltinRolesRejectUpdateDelete(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	seeded, err := repo.GetRoleByName(ctx, "sam_manager")
	if err != nil {
		t.Fatalf("GetRoleByName(sam_manager): %v", err)
	}
	if seeded == nil || !seeded.Builtin {
		t.Fatalf("expected seeded built-in sam_manager, got %+v", seeded)
	}

	hijack := &RBACRole{ID: seeded.ID, Name: "hijacked", CreatedBy: "system", UpdatedBy: "attacker"}
	if err = repo.UpdateRole(ctx, hijack, []string{"rbac:_:*"}, nil); !errors.Is(err, ErrRBACNotFound) {
		t.Errorf("UpdateRole(builtin) err = %v, want ErrRBACNotFound", err)
	}
	if err = repo.DeleteRole(ctx, seeded.ID); !errors.Is(err, ErrRBACNotFound) {
		t.Errorf("DeleteRole(builtin) err = %v, want ErrRBACNotFound", err)
	}

	after, _, _, err := repo.GetRoleByID(ctx, seeded.ID)
	if err != nil {
		t.Fatalf("GetRoleByID after guarded writes: %v", err)
	}
	if after == nil || after.Name != "sam_manager" || !after.Builtin {
		t.Errorf("built-in role mutated/deleted despite guard: %+v", after)
	}
}

func TestRBACRepo_RoleInheritance(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	parent := &RBACRole{Name: "sam_user", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, parent, []string{"agent_builder:*:read"}, nil); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	child := &RBACRole{Name: "sam_developer", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, child, []string{"agent_builder:_:create"}, []string{parent.ID}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	_, _, parents, err := repo.GetRoleByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if !slices.Equal(parents, []string{parent.ID}) {
		t.Errorf("parents = %v, want [%s]", parents, parent.ID)
	}

	if e := repo.UpdateRole(ctx, child, []string{"agent_builder:_:create"}, nil); e != nil {
		t.Fatalf("update to clear parents: %v", e)
	}
	_, _, parents, err = repo.GetRoleByID(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child after clear: %v", err)
	}
	if len(parents) != 0 {
		t.Errorf("expected no parents after update, got %v", parents)
	}

	selfRole := &RBACRole{Name: "self_loop", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, selfRole, nil, nil); err != nil {
		t.Fatalf("create self_loop: %v", err)
	}
	if err := repo.UpdateRole(ctx, selfRole, nil, []string{selfRole.ID}); err == nil {
		t.Fatal("expected CHECK violation for self-inheritance")
	}
}

func TestRBACRepo_DeleteRoleCascades(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	users := NewUsersRepo(db)
	ctx := context.Background()

	role := &RBACRole{Name: "to_delete", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, role, []string{"sam:test"}, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}

	user := mkPendingUser(ctx, t, users, "https://idp", "sub-123", "cascade@example.com")
	if err := repo.AddUserRole(ctx, user.ID, role.ID, "sys"); err != nil {
		t.Fatalf("add user role: %v", err)
	}
	mapping := &RBACIdpClaimMapping{
		OIDCProvider: "azure",
		ClaimValue:   "engineers",
		Name:         "Engineers",
		CreatedBy:    "sys",
		UpdatedBy:    "sys",
	}
	if err := repo.CreateClaimMapping(ctx, mapping, []string{role.ID}); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if err := repo.SetDefaultRoles(ctx, []string{role.ID}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}

	if err := repo.DeleteRole(ctx, role.ID); err != nil {
		t.Fatalf("delete role: %v", err)
	}

	userRoles, err := repo.ListUserRoles(ctx)
	if err != nil {
		t.Fatalf("list user roles: %v", err)
	}
	if len(userRoles) != 0 {
		t.Errorf("expected cascade to clear user roles, got %d", len(userRoles))
	}
	gotMapping, gotRoles, err := repo.GetClaimMappingByID(ctx, mapping.ID)
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	if gotMapping == nil {
		t.Fatalf("expected mapping entity to survive role delete")
	}
	if len(gotRoles) != 0 {
		t.Errorf("expected cascade to clear the mapping's role join rows, got %d", len(gotRoles))
	}
	defaults, err := repo.ListDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if len(defaults) != 0 {
		t.Errorf("expected cascade to clear defaults, got %d", len(defaults))
	}
}

func TestRBACRepo_UserRoleCRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	users := NewUsersRepo(db)
	ctx := context.Background()

	role := &RBACRole{Name: "sam_user", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}
	role2 := &RBACRole{Name: "sam_admin", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, role2, nil, nil); err != nil {
		t.Fatalf("create role2: %v", err)
	}

	user := mkPendingUser(ctx, t, users, "https://idp", "sub-abc", "abc@example.com")
	if err := repo.AddUserRole(ctx, user.ID, role.ID, "sys"); err != nil {
		t.Fatalf("add first grant: %v", err)
	}
	if err := repo.AddUserRole(ctx, user.ID, role2.ID, "sys"); err != nil {
		t.Fatalf("add second grant: %v", err)
	}

	if err := repo.AddUserRole(ctx, user.ID, role.ID, "sys"); err != nil {
		t.Fatalf("re-add same grant should be idempotent: %v", err)
	}

	forUser, err := repo.ListRolesForUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("list roles for user: %v", err)
	}
	if len(forUser) != 2 {
		t.Errorf("expected 2 grants, got %d", len(forUser))
	}

	all, err := repo.ListUserRoles(ctx)
	if err != nil {
		t.Fatalf("list all user roles: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 total grants, got %d", len(all))
	}

	target := forUser[0].ID
	if err := repo.DeleteUserRole(ctx, target); err != nil {
		t.Fatalf("delete grant: %v", err)
	}
	if err := repo.DeleteUserRole(ctx, target); !errors.Is(err, ErrRBACNotFound) {
		t.Fatalf("second delete err = %v, want ErrRBACNotFound", err)
	}
}

func TestRBACRepo_ClaimMappingCRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	role := &RBACRole{Name: "sam_developer", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}
	role2 := &RBACRole{Name: "sam_viewer", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, role2, nil, nil); err != nil {
		t.Fatalf("create role2: %v", err)
	}

	m := &RBACIdpClaimMapping{
		OIDCProvider: "azure",
		ClaimValue:   "engineers",
		Name:         "Engineers",
		CreatedBy:    "sys",
		UpdatedBy:    "sys",
	}
	if err := repo.CreateClaimMapping(ctx, m, []string{role.ID}); err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	dup := *m
	dup.ID = ""
	if err := repo.CreateClaimMapping(ctx, &dup, []string{role.ID}); err == nil {
		t.Fatal("expected UNIQUE violation on duplicate (oidc_provider, claim_value)")
	}

	if err := repo.UpdateClaimMapping(ctx, m, []string{role.ID, role2.ID}); err != nil {
		t.Fatalf("update mapping: %v", err)
	}
	got, gotRoles, err := repo.GetClaimMappingByID(ctx, m.ID)
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	if got == nil {
		t.Fatalf("mapping not found after update")
	}
	slices.Sort(gotRoles)
	want := []string{role.ID, role2.ID}
	slices.Sort(want)
	if !slices.Equal(gotRoles, want) {
		t.Fatalf("mapping roles after update = %v, want %v", gotRoles, want)
	}

	if err := repo.DeleteClaimMapping(ctx, m.ID); err != nil {
		t.Fatalf("delete mapping: %v", err)
	}
	if err := repo.DeleteClaimMapping(ctx, m.ID); err == nil {
		t.Fatal("expected delete-not-found")
	}
}

func TestRBACRepo_DefaultRolesReplaceSet(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	r1 := &RBACRole{Name: "sam_user", CreatedBy: "sys", UpdatedBy: "sys"}
	r2 := &RBACRole{Name: "sam_viewer", CreatedBy: "sys", UpdatedBy: "sys"}
	r3 := &RBACRole{Name: "sam_admin", CreatedBy: "sys", UpdatedBy: "sys"}
	for _, role := range []*RBACRole{r1, r2, r3} {
		if err := repo.CreateRole(ctx, role, nil, nil); err != nil {
			t.Fatalf("create role %s: %v", role.Name, err)
		}
	}

	if err := repo.SetDefaultRoles(ctx, []string{r1.ID, r2.ID}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	got, err := repo.ListDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("list default roles: %v", err)
	}
	slices.Sort(got)
	want := []string{r1.ID, r2.ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("defaults = %v, want %v", got, want)
	}

	if e := repo.SetDefaultRoles(ctx, []string{r3.ID}); e != nil {
		t.Fatalf("replace default roles: %v", e)
	}
	got, err = repo.ListDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("list after replace: %v", err)
	}
	if !slices.Equal(got, []string{r3.ID}) {
		t.Errorf("defaults after replace = %v, want [%s]", got, r3.ID)
	}

	if e := repo.SetDefaultRoles(ctx, nil); e != nil {
		t.Fatalf("clear default roles: %v", e)
	}
	got, err = repo.ListDefaultRoles(ctx)
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty defaults, got %v", got)
	}
}

func TestRBACRepo_Snapshot(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	users := NewUsersRepo(db)
	ctx := context.Background()

	admin := &RBACRole{Name: "sam_admin", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, admin, []string{"*"}, nil); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	userRole := &RBACRole{Name: "sam_user", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := repo.CreateRole(ctx, userRole, []string{"agent_builder:*:read"}, []string{admin.ID}); err != nil {
		t.Fatalf("create user role: %v", err)
	}

	alice := mkPendingUser(ctx, t, users, "https://idp", "sub-alice", "alice@example.com")
	if err := repo.AddUserRole(ctx, alice.ID, admin.ID, "sys"); err != nil {
		t.Fatalf("add user role: %v", err)
	}
	mapping := &RBACIdpClaimMapping{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		ClaimValue:   "engineers",
		Name:         "Engineers",
		CreatedBy:    "sys",
		UpdatedBy:    "sys",
	}
	if err := repo.CreateClaimMapping(ctx, mapping, []string{userRole.ID}); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if err := repo.SetDefaultRoles(ctx, []string{userRole.ID}); err != nil {
		t.Fatalf("set defaults: %v", err)
	}

	snap, err := repo.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	userRoles := 0
	for _, r := range snap.Roles {
		if !r.Builtin {
			userRoles++
		}
	}
	if userRoles != 2 {
		t.Errorf("snapshot user roles = %d, want 2 (excluding built-in seed)", userRoles)
	}
	if got := snap.RoleScopes[admin.ID]; !slices.Equal(got, []string{"*"}) {
		t.Errorf("admin scopes = %v, want [sam:*]", got)
	}
	if got := snap.RoleInheritance[userRole.ID]; !slices.Equal(got, []string{admin.ID}) {
		t.Errorf("user parents = %v, want [%s]", got, admin.ID)
	}
	if len(snap.UserRoles) != 1 || snap.UserRoles[0].UserID != alice.ID {
		t.Errorf("snapshot user roles = %+v", snap.UserRoles)
	}
	if len(snap.ClaimMappings) != 1 || snap.ClaimMappings[0].ClaimValue != "engineers" {
		t.Errorf("snapshot claim mappings = %+v", snap.ClaimMappings)
	}
	if snap.ClaimMappings[0].ClaimKey != "groups" {
		t.Errorf("snapshot claim mapping claim_key = %q, want groups", snap.ClaimMappings[0].ClaimKey)
	}
	if got := snap.ClaimMappingRoles[mapping.ID]; !slices.Equal(got, []string{userRole.ID}) {
		t.Errorf("snapshot claim mapping roles = %v, want [%s]", got, userRole.ID)
	}
	if !slices.Equal(snap.DefaultRoleIDs, []string{userRole.ID}) {
		t.Errorf("snapshot defaults = %v, want [%s]", snap.DefaultRoleIDs, userRole.ID)
	}
}

func TestRBACRepo_TransactionRollback(t *testing.T) {
	db := TestDB(t)
	repo := NewRBACRepo(db)
	ctx := context.Background()

	bogus := &RBACRole{Name: "orphan", CreatedBy: "sys", UpdatedBy: "sys"}
	err := repo.CreateRole(ctx, bogus, []string{"sam:test"}, []string{"00000000-0000-0000-0000-000000000000"})
	if err == nil {
		t.Fatal("expected FK violation when creating role with bogus parent")
	}
	got, err := repo.GetRoleByName(ctx, "orphan")
	if err != nil {
		t.Fatalf("get after rollback: %v", err)
	}
	if got != nil {
		t.Fatal("expected role insert to be rolled back")
	}
}
