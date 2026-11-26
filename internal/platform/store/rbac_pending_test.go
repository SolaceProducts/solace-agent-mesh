// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"slices"
	"testing"
)

func mkPendingRole(ctx context.Context, t *testing.T, rr *RBACRepo, name string) string {
	t.Helper()
	role := &RBACRole{Name: name, CreatedBy: "sys", UpdatedBy: "sys"}
	if err := rr.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
	return role.ID
}

func mkPendingUser(ctx context.Context, t *testing.T, ur *UsersRepo, issuer, sub, email string) *User {
	t.Helper()
	u := &User{Issuer: issuer, Sub: sub, CreatedBy: "sys", UpdatedBy: "sys"}
	if email != "" {
		u.Email = &email
	}
	if err := ur.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed user %s: %v", sub, err)
	}
	return u
}

func pendingRoleIDs(ctx context.Context, t *testing.T, rr *RBACRepo, userID string) []string {
	t.Helper()
	rows, err := rr.ListRolesForUser(ctx, userID)
	if err != nil {
		t.Fatalf("list concrete grants: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.RoleID)
	}
	slices.Sort(ids)
	return ids
}

func TestRBACRepo_MaterializePending_ClaimsProvenanceOnConflict(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")
	if err := rr.AddUserRole(ctx, u.ID, role, "admin"); err != nil {
		t.Fatalf("add interactive grant: %v", err)
	}
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectEmail, SubjectValue: "alice@example.com",
		RoleID: role, CreatedBy: "admin", UpdatedBy: "admin",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(granted) != 0 {
		t.Errorf("granted = %v, want empty (role already held via interactive grant)", granted)
	}
	pend, err := rr.ListPendingUserRoles(ctx)
	if err != nil || len(pend) != 0 {
		t.Fatalf("pending after materialize = %d (err %v), want 0 (consumed)", len(pend), err)
	}
	rows, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concrete grants = %d (err %v), want 1", len(rows), err)
	}
	if got := rows[0]; got.SubjectType != PendingSubjectEmail || got.SubjectValue != "alice@example.com" {
		t.Errorf("provenance not claimed on conflict: type=%q value=%q, want email/alice@example.com", got.SubjectType, got.SubjectValue)
	}
}

func TestRBACRepo_MaterializePending_Sub(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-123", "alice@example.com")
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectSub, SubjectValue: "sub-123", Issuer: "https://idp",
		RoleID: role, CreatedBy: "admin", UpdatedBy: "admin",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !slices.Equal(granted, []string{role}) {
		t.Fatalf("granted = %v, want [%s]", granted, role)
	}
	if got := pendingRoleIDs(ctx, t, rr, u.ID); !slices.Equal(got, []string{role}) {
		t.Fatalf("concrete grants = %v, want [%s]", got, role)
	}
	remaining, _ := rr.ListPendingUserRoles(ctx)
	if len(remaining) != 0 {
		t.Fatalf("pending not consumed: %d remain", len(remaining))
	}
}

func TestRBACRepo_AddUserRoleWithSubject_UpgradesEmptyProvenance(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	if err := rr.AddUserRole(ctx, u.ID, role, "admin"); err != nil {
		t.Fatalf("interactive grant: %v", err)
	}
	if _, err := rr.AddUserRoleWithSubject(ctx, u.ID, role, "cfg", RoleGrantSubject{Type: PendingSubjectSub, Value: "sub-1", Issuer: "https://idp"}); err != nil {
		t.Fatalf("declarative claim: %v", err)
	}
	rows, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v, want 1 grant", rows, err)
	}
	if rows[0].SubjectType != PendingSubjectSub || rows[0].SubjectValue != "sub-1" || rows[0].Issuer != "https://idp" {
		t.Fatalf("provenance not claimed on conflict: %+v", rows[0])
	}

	if _, err := rr.AddUserRoleWithSubject(ctx, u.ID, role, "cfg2", RoleGrantSubject{Type: PendingSubjectEmail, Value: "other@example.com"}); err != nil {
		t.Fatalf("second claim: %v", err)
	}
	rows, _ = rr.ListRolesForUser(ctx, u.ID)
	if rows[0].SubjectType != PendingSubjectSub || rows[0].SubjectValue != "sub-1" {
		t.Errorf("existing provenance must not be overwritten: %+v", rows[0])
	}
}

func TestRBACRepo_MaterializePending_PreservesProvenance(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-123", "alice@example.com")
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectSub, SubjectValue: "sub-123", Issuer: "https://idp",
		RoleID: role, CreatedBy: "admin", UpdatedBy: "admin",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	pendings, err := rr.ListPendingUserRoles(ctx)
	if err != nil || len(pendings) != 1 {
		t.Fatalf("list pending: got %d rows, err %v", len(pendings), err)
	}
	pend := pendings[0]
	if _, mErr := rr.MaterializePendingForUser(ctx, u, "login"); mErr != nil {
		t.Fatalf("materialize: %v", mErr)
	}
	rows, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("list roles for user: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 concrete grant, got %d", len(rows))
	}
	got := rows[0]
	if got.SubjectType != PendingSubjectSub || got.SubjectValue != "sub-123" || got.Issuer != "https://idp" {
		t.Errorf("provenance lost in materialization: type=%q value=%q issuer=%q", got.SubjectType, got.SubjectValue, got.Issuer)
	}
	if got.CreatedBy != pend.CreatedBy || got.CreatedTime != pend.CreatedTime {
		t.Errorf("authoring metadata not carried: createdBy=%q/createdTime=%d, want %q/%d from the pending grant",
			got.CreatedBy, got.CreatedTime, pend.CreatedBy, pend.CreatedTime)
	}
	if got.UpdatedBy != "login" {
		t.Errorf("updatedBy = %q, want the materialization actor %q", got.UpdatedBy, "login")
	}
}

func TestRBACRepo_MaterializePending_EmailCaseInsensitive(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "Alice@Example.com")
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectEmail, SubjectValue: "alice@example.com",
		RoleID: role, CreatedBy: "admin", UpdatedBy: "admin",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !slices.Equal(granted, []string{role}) {
		t.Fatalf("granted = %v, want [%s]", granted, role)
	}
}

func TestRBACRepo_MaterializePending_LegacyEmailPreferred(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	roleEmail := mkPendingRole(ctx, t, rr, "byEmail")
	roleSub := mkPendingRole(ctx, t, rr, "bySub")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-xyz", "alice@example.com")

	for _, p := range []*RBACPendingUserRole{
		{SubjectType: PendingSubjectLegacy, SubjectValue: "alice@example.com", RoleID: roleEmail, CreatedBy: "a", UpdatedBy: "a"},
		{SubjectType: PendingSubjectLegacy, SubjectValue: "sub-xyz", RoleID: roleSub, CreatedBy: "a", UpdatedBy: "a"},
	} {
		if err := rr.CreatePendingUserRole(ctx, p); err != nil {
			t.Fatalf("create pending: %v", err)
		}
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !slices.Equal(granted, []string{roleEmail}) {
		t.Fatalf("granted = %v, want only the email-keyed role %s", granted, roleEmail)
	}
	remaining, _ := rr.ListPendingUserRoles(ctx)
	if len(remaining) != 1 || remaining[0].RoleID != roleSub {
		t.Fatalf("expected the sub-keyed legacy grant to remain pending, got %+v", remaining)
	}
}

func TestRBACRepo_MaterializePending_LegacySubFallback(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-only", "")
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectLegacy, SubjectValue: "sub-only",
		RoleID: role, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if !slices.Equal(granted, []string{role}) {
		t.Fatalf("granted = %v, want [%s]", granted, role)
	}
}

func TestRBACRepo_MaterializePending_NonMatchUntouched(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-me", "me@example.com")
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectEmail, SubjectValue: "someone-else@example.com",
		RoleID: role, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(granted) != 0 {
		t.Fatalf("granted = %v, want none", granted)
	}
	if remaining, _ := rr.ListPendingUserRoles(ctx); len(remaining) != 1 {
		t.Fatalf("non-matching pending grant should remain, got %d", len(remaining))
	}
}

func TestRBACRepo_MaterializePending_Idempotent(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := context.Background()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")
	if err := rr.SetUserRoles(ctx, u.ID, []string{role}, "admin"); err != nil {
		t.Fatalf("pre-existing concrete grant: %v", err)
	}
	if err := rr.CreatePendingUserRole(ctx, &RBACPendingUserRole{
		SubjectType: PendingSubjectEmail, SubjectValue: "alice@example.com",
		RoleID: role, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	granted, err := rr.MaterializePendingForUser(ctx, u, "login")
	if err != nil {
		t.Fatalf("materialize (idempotent): %v", err)
	}
	if len(granted) != 0 {
		t.Errorf("granted = %v, want empty — the role was already held, so no new grant was created", granted)
	}
	if got := pendingRoleIDs(ctx, t, rr, u.ID); !slices.Equal(got, []string{role}) {
		t.Fatalf("concrete grants = %v, want single [%s]", got, role)
	}
	if remaining, _ := rr.ListPendingUserRoles(ctx); len(remaining) != 0 {
		t.Fatalf("pending not consumed: %d remain", len(remaining))
	}
}
