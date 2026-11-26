// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"testing"
	"time"
)

func TestRBACRepo_SetUserRolesWithSubject_UnchangedLeavesRowUntouched(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := t.Context()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	if _, err := rr.AddUserRoleWithSubject(ctx, u.ID, role, "cfg", RoleGrantSubject{Type: PendingSubjectEmail, Value: "alice@example.com"}); err != nil {
		t.Fatalf("seed declarative grant: %v", err)
	}
	before, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("before=%v err=%v, want 1 grant", before, err)
	}

	if err = rr.SetUserRoles(ctx, u.ID, []string{role}, "someone-else"); err != nil {
		t.Fatalf("re-submit unchanged role set: %v", err)
	}

	after, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("after=%v err=%v, want 1 grant", after, err)
	}
	if after[0].ID != before[0].ID {
		t.Errorf("id churned on a no-op re-submit: before=%s after=%s", before[0].ID, after[0].ID)
	}
	if after[0].SubjectType != PendingSubjectEmail || after[0].SubjectValue != "alice@example.com" {
		t.Errorf("subject wiped on a no-op re-submit: %+v", after[0])
	}
	if after[0].CreatedBy != before[0].CreatedBy {
		t.Errorf("created_by reset on a no-op re-submit: before=%s after=%s", before[0].CreatedBy, after[0].CreatedBy)
	}
	if after[0].CreatedTime != before[0].CreatedTime {
		t.Errorf("created_time reset on a no-op re-submit: before=%d after=%d", before[0].CreatedTime, after[0].CreatedTime)
	}
}

func TestRBACRepo_SetUserRolesWithSubject_AddOnlyInsertsNewRole(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := t.Context()

	roleA := mkPendingRole(ctx, t, rr, "role-a")
	roleB := mkPendingRole(ctx, t, rr, "role-b")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	if _, err := rr.AddUserRoleWithSubject(ctx, u.ID, roleA, "cfg", RoleGrantSubject{Type: PendingSubjectEmail, Value: "alice@example.com"}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	before, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("before=%v err=%v, want 1 grant", before, err)
	}

	if err = rr.SetUserRoles(ctx, u.ID, []string{roleA, roleB}, "admin"); err != nil {
		t.Fatalf("add role: %v", err)
	}

	after, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(after) != 2 {
		t.Fatalf("after=%v err=%v, want 2 grants", after, err)
	}
	for _, row := range after {
		if row.RoleID == roleA {
			if row.ID != before[0].ID {
				t.Errorf("existing role's grant id churned: before=%s after=%s", before[0].ID, row.ID)
			}
			if row.SubjectType != PendingSubjectEmail || row.SubjectValue != "alice@example.com" {
				t.Errorf("existing role's subject wiped: %+v", row)
			}
		}
		if row.RoleID == roleB && row.SubjectType != "" {
			t.Errorf("newly-added interactive role must carry the caller's (empty) subject, got %+v", row)
		}
	}
}

func TestRBACRepo_SetUserRolesWithSubject_RemoveOnlyDeletesRemovedRole(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := t.Context()

	roleA := mkPendingRole(ctx, t, rr, "role-a")
	roleB := mkPendingRole(ctx, t, rr, "role-b")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	if _, err := rr.AddUserRoleWithSubject(ctx, u.ID, roleA, "cfg", RoleGrantSubject{Type: PendingSubjectEmail, Value: "alice@example.com"}); err != nil {
		t.Fatalf("seed grant a: %v", err)
	}
	if err := rr.AddUserRole(ctx, u.ID, roleB, "admin"); err != nil {
		t.Fatalf("seed grant b: %v", err)
	}
	before, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(before) != 2 {
		t.Fatalf("before=%v err=%v, want 2 grants", before, err)
	}
	var keptBefore *RBACUserRole
	for _, row := range before {
		if row.RoleID == roleA {
			keptBefore = row
		}
	}
	if keptBefore == nil {
		t.Fatalf("seeded role-a grant missing from %v", before)
	}

	if err = rr.SetUserRoles(ctx, u.ID, []string{roleA}, "admin"); err != nil {
		t.Fatalf("remove role: %v", err)
	}

	after, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("after=%v err=%v, want 1 grant", after, err)
	}
	if after[0].RoleID != roleA {
		t.Fatalf("wrong role survived: %+v", after[0])
	}
	if after[0].ID != keptBefore.ID {
		t.Errorf("surviving role's grant id churned: before=%s after=%s", keptBefore.ID, after[0].ID)
	}
	if after[0].SubjectType != PendingSubjectEmail || after[0].SubjectValue != "alice@example.com" {
		t.Errorf("surviving role's subject wiped: %+v", after[0])
	}
}

func TestRBACRepo_SetUserRolesWithSubject_EmptyRoleIDsDeletesAll(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := t.Context()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")
	if err := rr.AddUserRole(ctx, u.ID, role, "admin"); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	if err := rr.SetUserRoles(ctx, u.ID, nil, "admin"); err != nil {
		t.Fatalf("clear roles: %v", err)
	}

	after, err := rr.ListRolesForUser(ctx, u.ID)
	if err != nil || len(after) != 0 {
		t.Fatalf("after=%v err=%v, want 0 grants", after, err)
	}
}

func TestRBACRepo_SetUserRolesWithSubject_DuplicateRoleIDsCollapse(t *testing.T) {
	db := TestDB(t)
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := t.Context()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")

	if err := rr.SetUserRoles(ctx, u.ID, []string{role, role}, "admin"); err != nil {
		t.Fatalf("set with duplicate role ids: %v", err)
	}

	got := pendingRoleIDs(ctx, t, rr, u.ID)
	if !slices.Equal(got, []string{role}) {
		t.Fatalf("grants = %v, want [%s]", got, role)
	}
}

func TestRBACRepo_SetUserRolesWithSubject_LocksUserRowOnPostgres(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("row locking only matters on postgres; sqlite serializes the whole write inside WithTx")
	}
	rr := NewRBACRepo(db)
	ur := NewUsersRepo(db)
	ctx := t.Context()

	role := mkPendingRole(ctx, t, rr, "admin")
	u := mkPendingUser(ctx, t, ur, "https://idp", "sub-1", "alice@example.com")
	if err := rr.AddUserRole(ctx, u.ID, role, "admin"); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	holder, err := db.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	defer func() { _ = holder.Rollback() }()
	var locked string
	if err := holder.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, u.ID).Scan(&locked); err != nil {
		t.Fatalf("acquire user lock: %v", err)
	}

	done := make(chan struct{})
	contendedErr := make(chan error, 1)
	start := time.Now()
	go func() {
		defer close(done)
		contendedErr <- rr.SetUserRoles(ctx, u.ID, nil, "admin")
	}()

	const holdFor = 200 * time.Millisecond
	time.Sleep(holdFor)
	select {
	case <-done:
		t.Fatalf("SetUserRoles completed while the user row was still locked — the FOR UPDATE lock did not serialize the writer")
	default:
	}
	if err := holder.Commit(); err != nil {
		t.Fatalf("release lock: %v", err)
	}

	<-done
	if err := <-contendedErr; err != nil {
		t.Fatalf("SetUserRoles under contention: %v", err)
	}
	if elapsed := time.Since(start); elapsed < holdFor {
		t.Errorf("SetUserRoles returned after %s, want it to block until the holder released the lock at ~%s", elapsed, holdFor)
	}

	got := pendingRoleIDs(ctx, t, rr, u.ID)
	if !slices.Equal(got, nil) {
		t.Fatalf("grants after contended write = %v, want none", got)
	}
}
