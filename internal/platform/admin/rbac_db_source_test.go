// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestRBACDBSource_TranslatesSnapshot(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	ctx := context.Background()

	admin := &store.RBACRole{Name: "admin", Description: "full", CreatedBy: "t", UpdatedBy: "t"}
	if err := repo.CreateRole(ctx, admin, []string{"*"}, nil); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	user := &store.RBACRole{Name: "user", CreatedBy: "t", UpdatedBy: "t"}
	if err := repo.CreateRole(ctx, user, []string{"agent_builder:*:read"}, []string{admin.ID}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	alice := &store.User{Issuer: "iss", Sub: "alice", CreatedBy: "t", UpdatedBy: "t"}
	if err := usersRepo.UpsertUser(ctx, alice, nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := repo.AddUserRole(ctx, alice.ID, admin.ID, "t"); err != nil {
		t.Fatalf("add user role: %v", err)
	}
	if err := repo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{
		OIDCProvider: "azure", ClaimKey: "groups", ClaimValue: "admins-id", Name: "Admins",
		CreatedBy: "t", UpdatedBy: "t",
	}, []string{admin.ID}); err != nil {
		t.Fatalf("create claim mapping: %v", err)
	}
	if err := repo.SetDefaultRoles(ctx, []string{user.ID}); err != nil {
		t.Fatalf("set default roles: %v", err)
	}

	state, err := newRBACDBSource(repo).Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	roles := state.Roles[:0:0]
	for _, r := range state.Roles {
		if r.Name != builtinSeedRoleName {
			roles = append(roles, r)
		}
	}
	if len(roles) != 2 {
		t.Fatalf("got %d user roles, want 2", len(roles))
	}
	for _, r := range roles {
		switch r.Name {
		case "admin":
			if !slices.Equal(r.Scopes, []string{"*"}) {
				t.Errorf("admin scopes = %v, want [sam:*]", r.Scopes)
			}
			if len(r.Parents) != 0 {
				t.Errorf("admin parents = %v, want []", r.Parents)
			}
		case "user":
			if !slices.Equal(r.Scopes, []string{"agent_builder:*:read"}) {
				t.Errorf("user scopes = %v, want [agent_builder:*:read]", r.Scopes)
			}
			if !slices.Equal(r.Parents, []string{admin.ID}) {
				t.Errorf("user parents = %v, want [%s]", r.Parents, admin.ID)
			}
		default:
			t.Errorf("unexpected role %q", r.Name)
		}
	}

	if len(state.UserRoles) != 1 || state.UserRoles[0].UserID != alice.ID || state.UserRoles[0].RoleID != admin.ID {
		t.Errorf("user roles = %+v", state.UserRoles)
	}
	if len(state.ClaimMappings) != 1 || state.ClaimMappings[0].ClaimValue != "admins-id" || !slices.Equal(state.ClaimMappings[0].RoleIDs, []string{admin.ID}) {
		t.Errorf("claim mappings = %+v", state.ClaimMappings)
	}
	if state.ClaimMappings[0].ClaimKey != "groups" {
		t.Errorf("claim mapping claim_key = %q, want groups", state.ClaimMappings[0].ClaimKey)
	}
	if !slices.Equal(state.DefaultRoleIDs, []string{user.ID}) {
		t.Errorf("default role ids = %v, want [%s]", state.DefaultRoleIDs, user.ID)
	}
}

func TestRBACDBSource_EmptySnapshot(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)

	state, err := newRBACDBSource(repo).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, r := range state.Roles {
		if r.Name != builtinSeedRoleName {
			t.Errorf("unexpected non-builtin role %q in otherwise-empty snapshot", r.Name)
		}
	}
	if len(state.UserRoles) != 0 || len(state.ClaimMappings) != 0 || len(state.DefaultRoleIDs) != 0 {
		t.Errorf("expected empty user snapshot, got %+v", state)
	}
}

const builtinSeedRoleName = "sam_manager"
