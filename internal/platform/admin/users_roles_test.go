// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestUsersService_SetAndGetUserRoleGrants(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleA := seedRole(ctx, t, rbacRepo, composer, "a")
	roleB := seedRole(ctx, t, rbacRepo, composer, "b")
	roleC := seedRole(ctx, t, rbacRepo, composer, "c")
	uid := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "{}", nil)

	if _, err := svc.SetUserRoleGrants(ctx, uid, []string{roleA, roleB}, "admin", []string{"*"}); err != nil {
		t.Fatalf("set {a,b}: %v", err)
	}
	assertUserRoles(ctx, t, svc, uid, []string{roleA, roleB})

	if _, err := svc.SetUserRoleGrants(ctx, uid, []string{roleB, roleC}, "admin", []string{"*"}); err != nil {
		t.Fatalf("set {b,c}: %v", err)
	}
	assertUserRoles(ctx, t, svc, uid, []string{roleB, roleC})

	if _, err := svc.SetUserRoleGrants(ctx, uid, nil, "admin", []string{"*"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	assertUserRoles(ctx, t, svc, uid, []string{})
}

func TestUsersService_SetUserRoleGrants_UnchangedPreservesProvenance(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleA := seedRole(ctx, t, rbacRepo, composer, "a")
	uid := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "{}", nil)

	if _, err := svc.SetUserRoleGrants(ctx, uid, []string{roleA}, "admin", []string{"*"}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	before, err := rbacRepo.ListRolesForUser(ctx, uid)
	if err != nil || len(before) != 1 {
		t.Fatalf("before=%v err=%v, want 1 grant", before, err)
	}

	if _, err = svc.SetUserRoleGrants(ctx, uid, []string{roleA}, "someone-else", []string{"*"}); err != nil {
		t.Fatalf("re-submit unchanged role set: %v", err)
	}

	after, err := rbacRepo.ListRolesForUser(ctx, uid)
	if err != nil || len(after) != 1 {
		t.Fatalf("after=%v err=%v, want 1 grant", after, err)
	}
	if after[0].ID != before[0].ID {
		t.Errorf("id churned on a no-op re-submit: before=%s after=%s", before[0].ID, after[0].ID)
	}
	if after[0].CreatedBy != before[0].CreatedBy {
		t.Errorf("created_by reset on a no-op re-submit: before=%s after=%s", before[0].CreatedBy, after[0].CreatedBy)
	}
	if after[0].CreatedTime != before[0].CreatedTime {
		t.Errorf("created_time reset on a no-op re-submit: before=%d after=%d", before[0].CreatedTime, after[0].CreatedTime)
	}
}

func TestUsersService_SetUserRoles_Deduplicates(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleA := seedRole(ctx, t, rbacRepo, composer, "a")
	uid := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "{}", nil)

	resp, err := svc.SetUserRoleGrants(ctx, uid, []string{roleA, roleA, roleA}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if len(resp.RoleIDs) != 1 || resp.RoleIDs[0] != roleA {
		t.Fatalf("expected single grant %q, got %v", roleA, resp.RoleIDs)
	}
	assertUserRoles(ctx, t, svc, uid, []string{roleA})
}

func TestUsersService_SetUserRoles_UnknownRole(t *testing.T) {
	t.Parallel()
	svc, usersRepo, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()

	roleA := seedRole(ctx, t, rbacRepo, composer, "a")
	uid := seedUserRow(ctx, t, usersRepo, "s1", "Alice", "alice@example.com", "azure", "{}", nil)

	_, err := svc.SetUserRoleGrants(ctx, uid, []string{roleA, "not-a-role"}, "admin", []string{"*"})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	assertUserRoles(ctx, t, svc, uid, []string{})
}

func TestUsersService_UserRoles_UnknownUser(t *testing.T) {
	t.Parallel()
	svc, _, rbacRepo, composer := newUsersTestService(t)
	ctx := t.Context()
	roleA := seedRole(ctx, t, rbacRepo, composer, "a")

	missing := "00000000-0000-0000-0000-000000000000"

	_, getErr := svc.GetUserRoleGrants(ctx, missing)
	var nfe1 *NotFoundError
	if !errors.As(getErr, &nfe1) {
		t.Fatalf("GetUserRoleGrants: expected NotFoundError, got %v", getErr)
	}

	_, setErr := svc.SetUserRoleGrants(ctx, missing, []string{roleA}, "admin", []string{"*"})
	var nfe2 *NotFoundError
	if !errors.As(setErr, &nfe2) {
		t.Fatalf("SetUserRoles: expected NotFoundError, got %v", setErr)
	}
}

func assertUserRoles(ctx context.Context, t *testing.T, svc *UsersService, userID string, want []string) {
	t.Helper()
	resp, err := svc.GetUserRoleGrants(ctx, userID)
	if err != nil {
		t.Fatalf("get user roles: %v", err)
	}
	got := slices.Clone(resp.RoleIDs)
	slices.Sort(got)
	wantSorted := slices.Clone(want)
	slices.Sort(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Fatalf("roleIds = %v, want %v", got, wantSorted)
	}
}
