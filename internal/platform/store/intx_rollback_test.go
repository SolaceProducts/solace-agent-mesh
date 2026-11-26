// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var errRollback = errors.New("roll back")

func rollBack(t *testing.T, db *DB, write func(ctx context.Context, tx *dbutil.Tx) error) {
	t.Helper()
	ctx := t.Context()
	err := db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := write(ctx, tx); err != nil {
			t.Fatalf("write in tx: %v", err)
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("WithTx err = %v, want errRollback", err)
	}
}

type rbacFixture struct {
	db      *DB
	rbac    *RBACRepo
	users   *UsersRepo
	role    *RBACRole
	user    *User
	grantID string
	other   *User
	claim   *RBACIdpClaimMapping
}

func newRBACFixture(t *testing.T) *rbacFixture {
	t.Helper()
	ctx := t.Context()
	db := TestDB(t)
	f := &rbacFixture{db: db, rbac: NewRBACRepo(db), users: NewUsersRepo(db)}
	f.role = &RBACRole{Name: "viewer", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := f.rbac.CreateRole(ctx, f.role, []string{"sam:read"}, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}
	f.user = mkPendingUser(ctx, t, f.users, "https://idp", "sub-1", "one@example.com")
	grantID, err := f.rbac.AddUserRoleWithSubject(ctx, f.user.ID, f.role.ID, "sys", RoleGrantSubject{})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	f.grantID = grantID
	f.other = mkPendingUser(ctx, t, f.users, "https://idp", "sub-2", "two@example.com")
	f.claim = &RBACIdpClaimMapping{OIDCProvider: "idp", ClaimKey: "groups", ClaimValue: "eng", Name: "eng", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := f.rbac.CreateClaimMapping(ctx, f.claim, []string{f.role.ID}); err != nil {
		t.Fatalf("create claim mapping: %v", err)
	}
	return f
}

func (f *rbacFixture) snapshot(t *testing.T) *RBACSnapshot {
	t.Helper()
	snap, err := f.rbac.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snap
}

func TestRBACRepo_InTxWritesRollBackWithTheTransaction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		write func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error
	}{
		{"CreateRoleInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.CreateRoleInTx(ctx, tx, &RBACRole{Name: "new", CreatedBy: "sys", UpdatedBy: "sys"}, nil, nil)
		}},
		{"UpdateRoleInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.UpdateRoleInTx(ctx, tx, &RBACRole{ID: f.role.ID, Name: "renamed", UpdatedBy: "sys"}, []string{"sam:write"}, nil)
		}},
		{"DeleteRoleInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.DeleteRoleInTx(ctx, tx, f.role.ID)
		}},
		{"AddUserRoleWithSubjectInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			_, err := f.rbac.AddUserRoleWithSubjectInTx(ctx, tx, f.other.ID, f.role.ID, "sys", RoleGrantSubject{})
			return err
		}},
		{"DeleteUserRoleInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.DeleteUserRoleInTx(ctx, tx, f.grantID)
		}},
		{"SetUserRolesWithSubjectInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.SetUserRolesWithSubjectInTx(ctx, tx, f.user.ID, nil, "sys", RoleGrantSubject{})
		}},
		{"CreateClaimMappingInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			m := &RBACIdpClaimMapping{OIDCProvider: "idp", ClaimKey: "groups", ClaimValue: "ops", Name: "ops", CreatedBy: "sys", UpdatedBy: "sys"}
			return f.rbac.CreateClaimMappingInTx(ctx, tx, m, []string{f.role.ID})
		}},
		{"UpdateClaimMappingInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			m := *f.claim
			m.ClaimValue = "ops"
			return f.rbac.UpdateClaimMappingInTx(ctx, tx, &m, nil)
		}},
		{"DeleteClaimMappingInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.DeleteClaimMappingInTx(ctx, tx, f.claim.ID)
		}},
		{"SetDefaultRolesInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			return f.rbac.SetDefaultRolesInTx(ctx, tx, []string{f.role.ID})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRBACFixture(t)
			before := f.snapshot(t)
			rollBack(t, f.db, func(ctx context.Context, tx *dbutil.Tx) error {
				return tc.write(ctx, t, f, tx)
			})
			after := f.snapshot(t)
			if !sameRBACState(before, after) {
				t.Errorf("RBAC state changed after rollback:\nbefore %+v\nafter  %+v", before, after)
			}
		})
	}
}

func sameRBACState(a, b *RBACSnapshot) bool {
	if len(a.Roles) != len(b.Roles) || len(a.UserRoles) != len(b.UserRoles) ||
		len(a.ClaimMappings) != len(b.ClaimMappings) || len(a.DefaultRoleIDs) != len(b.DefaultRoleIDs) {
		return false
	}
	for i := range a.Roles {
		if a.Roles[i].Name != b.Roles[i].Name || len(a.RoleScopes[a.Roles[i].ID]) != len(b.RoleScopes[b.Roles[i].ID]) {
			return false
		}
	}
	for i := range a.ClaimMappings {
		if a.ClaimMappings[i].ClaimValue != b.ClaimMappings[i].ClaimValue ||
			len(a.ClaimMappingRoles[a.ClaimMappings[i].ID]) != len(b.ClaimMappingRoles[b.ClaimMappings[i].ID]) {
			return false
		}
	}
	return true
}

func TestRBACRepo_PendingInTxWritesRollBackWithTheTransaction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		write func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error
	}{
		{"DeletePendingUserRolesForSubjectInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			_, err := f.rbac.DeletePendingUserRolesForSubjectInTx(ctx, tx, PendingSubjectEmail, "later@example.com", "")
			return err
		}},
		{"MaterializePendingForUserInTx", func(ctx context.Context, t *testing.T, f *rbacFixture, tx *dbutil.Tx) error {
			later := &User{ID: f.other.ID, Issuer: f.other.Issuer, Sub: f.other.Sub, Email: new("later@example.com")}
			granted, err := f.rbac.MaterializePendingForUserInTx(ctx, tx, later, "sys")
			if len(granted) != 1 {
				t.Fatalf("granted = %v, want one role", granted)
			}
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRBACFixture(t)
			ctx := t.Context()
			p := &RBACPendingUserRole{SubjectType: PendingSubjectEmail, SubjectValue: "later@example.com", RoleID: f.role.ID, CreatedBy: "sys", UpdatedBy: "sys"}
			if err := f.rbac.CreatePendingUserRole(ctx, p); err != nil {
				t.Fatalf("create pending: %v", err)
			}
			before := f.snapshot(t)
			rollBack(t, f.db, func(ctx context.Context, tx *dbutil.Tx) error {
				return tc.write(ctx, t, f, tx)
			})
			pending, err := f.rbac.ListPendingUserRoles(ctx)
			if err != nil {
				t.Fatalf("list pending: %v", err)
			}
			if len(pending) != 1 {
				t.Errorf("pending grants after rollback = %d, want 1", len(pending))
			}
			if !sameRBACState(before, f.snapshot(t)) {
				t.Error("RBAC state changed after rollback")
			}
		})
	}
}

func TestUsersRepo_InTxWritesRollBackWithTheTransaction(t *testing.T) {
	t.Parallel()
	newSystemUser := func(t *testing.T, users *UsersRepo) *User {
		t.Helper()
		name := "before"
		u := &User{Issuer: "sam:system", Sub: "system:bot", ProvisionedVia: provisionedViaSystem, DisplayName: &name, CreatedBy: "sys", UpdatedBy: "sys"}
		if err := users.UpsertUser(t.Context(), u, nil); err != nil {
			t.Fatalf("seed system user: %v", err)
		}
		return u
	}

	t.Run("UpsertUserInTx", func(t *testing.T) {
		db := TestDB(t)
		users := NewUsersRepo(db)
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			return users.UpsertUserInTx(ctx, tx, &User{Issuer: "https://idp", Sub: "sub-new", CreatedBy: "sys", UpdatedBy: "sys"}, nil)
		})
		got, _, err := users.GetByIssuerSub(t.Context(), "https://idp", "sub-new")
		if err != nil || got != nil {
			t.Errorf("user after rollback = %v, %v; want none", got, err)
		}
	})
	t.Run("UpsertUserWithClaimsInTx", func(t *testing.T) {
		db := TestDB(t)
		users := NewUsersRepo(db)
		u := &User{Issuer: "https://idp", Sub: "sub-claims", CreatedBy: "sys", UpdatedBy: "sys"}
		if err := users.UpsertUser(t.Context(), u, nil); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		claims := IdentityClaimWrite{Replace: true, Claims: []UserIdentityClaim{{ClaimKey: "phone_number", ClaimValueCanonical: "15551234567", Verified: true}}}
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			return users.UpsertUserWithClaimsInTx(ctx, tx, u, nil, claims)
		})
		got, err := NewUserIdentityClaimsRepo(db).ClaimsForUser(t.Context(), u.ID)
		if err != nil || len(got) != 0 {
			t.Errorf("claims after rollback = %d, %v; want none", len(got), err)
		}
	})
	t.Run("GetByIssuerSubInTx reads the transaction", func(t *testing.T) {
		db := TestDB(t)
		users := NewUsersRepo(db)
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			if err := users.UpsertUserInTx(ctx, tx, &User{Issuer: "https://idp", Sub: "sub-new", CreatedBy: "sys", UpdatedBy: "sys"}, nil); err != nil {
				return err
			}
			got, err := users.GetByIssuerSubInTx(ctx, tx, "https://idp", "sub-new")
			if got == nil {
				t.Error("GetByIssuerSubInTx did not see the row written in its transaction")
			}
			return err
		})
	})
	t.Run("UpdateSystemUserInTx", func(t *testing.T) {
		db := TestDB(t)
		users := NewUsersRepo(db)
		u := newSystemUser(t, users)
		after := "after"
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			return users.UpdateSystemUserInTx(ctx, tx, u.ID, &after, "sys")
		})
		got, _, err := users.GetByID(t.Context(), u.ID)
		if err != nil || got == nil || got.DisplayName == nil || *got.DisplayName != "before" {
			t.Errorf("display name after rollback = %v, %v; want before", got, err)
		}
	})
	t.Run("DeleteSystemUserInTx", func(t *testing.T) {
		db := TestDB(t)
		users := NewUsersRepo(db)
		u := newSystemUser(t, users)
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			_, err := users.DeleteSystemUserInTx(ctx, tx, u.ID)
			return err
		})
		got, _, err := users.GetByID(t.Context(), u.ID)
		if err != nil || got == nil {
			t.Errorf("system user after rollback = %v, %v; want it kept", got, err)
		}
	})
}

func seedIdentity(t *testing.T, db *DB) (*User, *EntrypointIdentityBindingsRepo) {
	t.Helper()
	ctx := t.Context()
	users := NewUsersRepo(db)
	bindings := NewEntrypointIdentityBindingsRepo(db)
	u := &User{Issuer: "https://idp", Sub: "sub-phone", CreatedBy: "sys", UpdatedBy: "sys"}
	claims := IdentityClaimWrite{Replace: true, Claims: []UserIdentityClaim{{ClaimKey: "phone_number", ClaimValueCanonical: "15551234567", Verified: true}}}
	if err := users.UpsertUserWithClaims(ctx, u, nil, claims); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := bindings.ObserveContinuity(ctx, "gw", "phone_number", "15551234567", "tok-1", u.ID, 1); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(ctx, nil, "phone_number", "15551234567", u.ID, 1); err != nil {
		t.Fatalf("corroborate binding: %v", err)
	}
	return u, bindings
}

func TestUserIdentityClaimsRepo_InTx(t *testing.T) {
	t.Parallel()
	t.Run("DeleteForUserInTx rolls back", func(t *testing.T) {
		db := TestDB(t)
		u, _ := seedIdentity(t, db)
		claims := NewUserIdentityClaimsRepo(db)
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			_, err := claims.DeleteForUserInTx(ctx, tx, u.ID)
			return err
		})
		got, err := claims.ClaimsForUser(t.Context(), u.ID)
		if err != nil || len(got) != 1 {
			t.Errorf("claims after rollback = %d, %v; want 1", len(got), err)
		}
	})
	t.Run("ClaimsForUserInTx reads the transaction", func(t *testing.T) {
		db := TestDB(t)
		u, _ := seedIdentity(t, db)
		claims := NewUserIdentityClaimsRepo(db)
		rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
			if _, err := claims.DeleteForUserInTx(ctx, tx, u.ID); err != nil {
				return err
			}
			got, err := claims.ClaimsForUserInTx(ctx, tx, u.ID)
			if len(got) != 0 {
				t.Errorf("claims inside tx after delete = %d, want 0", len(got))
			}
			return err
		})
	})
}

func TestEntrypointIdentityBindingsRepo_InTxWritesRollBackWithTheTransaction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		write func(ctx context.Context, t *testing.T, b *EntrypointIdentityBindingsRepo, tx *dbutil.Tx, userID string) error
	}{
		{"DeleteForValuesInTx", func(ctx context.Context, t *testing.T, b *EntrypointIdentityBindingsRepo, tx *dbutil.Tx, _ string) error {
			_, err := b.DeleteForValuesInTx(ctx, tx, "phone_number", []string{"15551234567"})
			return err
		}},
		{"DeleteForUserInTx", func(ctx context.Context, t *testing.T, b *EntrypointIdentityBindingsRepo, tx *dbutil.Tx, userID string) error {
			_, err := b.DeleteForUserInTx(ctx, tx, userID)
			return err
		}},
		{"RebindInTx", func(ctx context.Context, t *testing.T, b *EntrypointIdentityBindingsRepo, tx *dbutil.Tx, _ string) error {
			applied, err := b.RebindInTx(ctx, tx, "gw", "phone_number", "15551234567", TokenRotation{Predecessor: "tok-1", Successor: "tok-2"}, 2)
			if !applied {
				t.Fatal("rebind did not apply inside the transaction")
			}
			return err
		}},
		{"DiscardInTx", func(ctx context.Context, t *testing.T, b *EntrypointIdentityBindingsRepo, tx *dbutil.Tx, userID string) error {
			applied, err := b.DiscardInTx(ctx, tx, "gw", "phone_number", "15551234567", userID, 2)
			if !applied {
				t.Fatal("discard did not apply inside the transaction")
			}
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := TestDB(t)
			u, bindings := seedIdentity(t, db)
			rollBack(t, db, func(ctx context.Context, tx *dbutil.Tx) error {
				return tc.write(ctx, t, bindings, tx, u.ID)
			})
			got, err := bindings.Get(t.Context(), "gw", "phone_number", "15551234567")
			if err != nil || got == nil {
				t.Fatalf("binding after rollback = %v, %v; want it kept", got, err)
			}
			if got.ContinuityToken != "tok-1" || got.RotatedAt != nil {
				t.Errorf("binding after rollback: token %q rotated %v; want tok-1 and not rotated", got.ContinuityToken, got.RotatedAt)
			}
		})
	}
}
