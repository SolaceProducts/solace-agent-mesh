// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var seededSystemUserIDs = map[string]string{
	"system:default": "6dbe83db-3549-4d99-b4fb-c3aea7b75219",
	"system:channel": "1f8ffb5d-fc1b-47b3-8578-559670c859ae",
	"system:eval":    "a38ff022-96f3-4a78-a2e5-8a71bfa882ab",
}

func TestSeedSystemUsersMigration_SeedsAndRollsBack(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "seed_system_users", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		repo := NewUsersRepo(db)
		assertSeeded := func(stage string) {
			t.Helper()
			for sub, id := range seededSystemUserIDs {
				u, _, err := repo.GetByIssuerSub(ctx, seededSystemUserIssuer, sub)
				if err != nil {
					t.Fatalf("%s: get %s: %v", stage, sub, err)
				}
				if u == nil || u.ID != id || u.ProvisionedVia != provisionedViaSystem || !u.Active || u.LastLoginAt != nil {
					t.Errorf("%s: %s = %+v, want id %s, provisioned via system, active, never logged in", stage, sub, u, id)
				}
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up: %v", err)
		}
		assertSeeded("after up")

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		n := 0
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			t.Fatalf("count after down: %v", err)
		}
		if n != 0 {
			t.Errorf("users after down = %d, want 0", n)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply: %v", err)
		}
		assertSeeded("after re-apply")
	})
}

func TestSeedSystemUsersMigration_UpgradesAPopulatedDirectory(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "seed_system_users", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		repo := NewUsersRepo(db)
		existing := []*User{
			{Issuer: "https://idp.example", Sub: "alice-sub", Email: new("alice@example.com"), DisplayName: new("Alice"), CreatedBy: "jit", UpdatedBy: "jit"},
			{Issuer: "https://idp.example", Sub: "bob-sub", Email: new("bob@example.com"), CreatedBy: "jit", UpdatedBy: "jit"},
			{Issuer: seededSystemUserIssuer, Sub: "system:orders-bot", DisplayName: new("Orders bot"), ProvisionedVia: provisionedViaSystem, CreatedBy: "admin", UpdatedBy: "admin"},
		}
		for _, u := range existing {
			if err := repo.UpsertUser(ctx, u, nil); err != nil {
				t.Fatalf("seed %s: %v", u.Sub, err)
			}
		}
		before := make(map[string]User, len(existing))
		for _, u := range existing {
			got, _, err := repo.GetByID(ctx, u.ID)
			if err != nil || got == nil {
				t.Fatalf("read %s before upgrade: %+v (err %v)", u.Sub, got, err)
			}
			before[u.ID] = *got
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up over a populated directory: %v", err)
		}

		for sub, id := range seededSystemUserIDs {
			if u, _, err := repo.GetByIssuerSub(ctx, seededSystemUserIssuer, sub); err != nil || u == nil || u.ID != id {
				t.Errorf("%s after upgrade = %+v (err %v), want seeded with id %s", sub, u, err, id)
			}
		}
		for id, want := range before {
			got, _, err := repo.GetByID(ctx, id)
			if err != nil || got == nil {
				t.Fatalf("read %s after upgrade: %+v (err %v)", want.Sub, got, err)
			}
			if got.Sub != want.Sub || got.Issuer != want.Issuer || got.ProvisionedVia != want.ProvisionedVia ||
				derefString(got.Email) != derefString(want.Email) || derefString(got.DisplayName) != derefString(want.DisplayName) ||
				got.UpdatedTime != want.UpdatedTime {
				t.Errorf("%s changed by the seed: before %+v, after %+v", want.Sub, want, *got)
			}
		}
		n := 0
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if want := len(existing) + len(seededSystemUserIDs); n != want {
			t.Errorf("users after upgrade = %d, want %d (existing + seeded)", n, want)
		}
	})
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestSeedSystemUsersMigration_KeepsExistingShadowRow(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "seed_system_users", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		repo := NewUsersRepo(db)
		shadow := &User{
			Issuer: seededSystemUserIssuer, Sub: "system:default", DisplayName: new("Shadow"),
			ProvisionedVia: provisionedViaSystem, CreatedBy: "admin", UpdatedBy: "admin",
		}
		if err := repo.UpsertUser(ctx, shadow, nil); err != nil {
			t.Fatalf("seed shadow row: %v", err)
		}
		if shadow.ID == seededSystemUserIDs["system:default"] {
			t.Fatal("shadow row got the seed id; the test needs a distinct one")
		}
		rbacRepo := NewRBACRepo(db)
		role := &RBACRole{Name: "shadow-role", CreatedBy: "admin", UpdatedBy: "admin"}
		if err := rbacRepo.CreateRole(ctx, role, nil, nil); err != nil {
			t.Fatalf("create role: %v", err)
		}
		if _, err := rbacRepo.AddUserRoleWithSubject(ctx, shadow.ID, role.ID, "admin", RoleGrantSubject{Type: PendingSubjectSub, Value: shadow.Sub, Issuer: shadow.Issuer}); err != nil {
			t.Fatalf("grant shadow row: %v", err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up over a shadow row: %v", err)
		}
		assertShadowIntact := func(stage string) {
			t.Helper()
			got, _, err := repo.GetByIssuerSub(ctx, seededSystemUserIssuer, "system:default")
			if err != nil {
				t.Fatalf("%s: get default: %v", stage, err)
			}
			if got == nil || got.ID != shadow.ID || got.DisplayName == nil || *got.DisplayName != "Shadow" {
				t.Errorf("%s: default row = %+v, want the untouched shadow row %s", stage, got, shadow.ID)
			}
			roles, err := rbacRepo.ListRolesForUser(ctx, shadow.ID)
			if err != nil || len(roles) != 1 {
				t.Errorf("%s: shadow grants = %v (err %v), want the one grant kept", stage, roles, err)
			}
		}
		assertShadowIntact("after up")
		if u, _, err := repo.GetByID(ctx, seededSystemUserIDs["system:default"]); err != nil || u != nil {
			t.Errorf("seed id row = %+v (err %v), want none — the conflict must skip the insert", u, err)
		}
		if u, _, err := repo.GetByID(ctx, seededSystemUserIDs["system:eval"]); err != nil || u == nil {
			t.Errorf("eval row = %+v (err %v), want seeded — one conflict must not skip the others", u, err)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		assertShadowIntact("after down")
	})
}

const evidenceSystemUserID = "6f919ea1-0bcc-45d7-825a-f6fdbb388c1e"

func TestSeedEvidenceSystemUserMigration_SeedsAndKeepsAnExistingRow(t *testing.T) {
	t.Parallel()
	t.Run("seeded", func(t *testing.T) {
		t.Parallel()
		forEachPlatformDialectAt(t, "seed_evidence_system_user", func(t *testing.T, db *DB, version int64) {
			ctx := t.Context()
			repo := NewUsersRepo(db)
			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate up to %d: %v", version-1, err)
			}
			before := countUsers(ctx, t, db)

			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("migrate up: %v", err)
			}
			u, _, err := repo.GetByIssuerSub(ctx, seededSystemUserIssuer, "system:evidence")
			if err != nil || u == nil || u.ID != evidenceSystemUserID || u.ProvisionedVia != provisionedViaSystem || !u.Active {
				t.Errorf("system:evidence = %+v (err %v), want id %s, provisioned via system, active", u, err, evidenceSystemUserID)
			}
			if after := countUsers(ctx, t, db); after != before+1 {
				t.Errorf("users after up = %d, want %d", after, before+1)
			}

			if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate down: %v", err)
			}
			if after := countUsers(ctx, t, db); after != before {
				t.Errorf("users after down = %d, want %d", after, before)
			}
		})
	})
	t.Run("existing row kept", func(t *testing.T) {
		t.Parallel()
		forEachPlatformDialectAt(t, "seed_evidence_system_user", func(t *testing.T, db *DB, version int64) {
			ctx := t.Context()
			repo := NewUsersRepo(db)
			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate up to %d: %v", version-1, err)
			}
			existing := &User{Issuer: seededSystemUserIssuer, Sub: "system:evidence", DisplayName: new("Operator's"),
				ProvisionedVia: provisionedViaSystem, CreatedBy: "admin", UpdatedBy: "admin"}
			if err := repo.UpsertUser(ctx, existing, nil); err != nil {
				t.Fatalf("seed existing row: %v", err)
			}

			for _, step := range []struct {
				name    string
				migrate func() error
			}{
				{"up", func() error { return dbutil.MigrateUpTo(ctx, db.migrateConfig(), version) }},
				{"down", func() error { return dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1) }},
			} {
				if err := step.migrate(); err != nil {
					t.Fatalf("migrate %s: %v", step.name, err)
				}
				got, _, err := repo.GetByIssuerSub(ctx, seededSystemUserIssuer, "system:evidence")
				if err != nil || got == nil || got.ID != existing.ID {
					t.Errorf("after %s: system:evidence = %+v (err %v), want the existing row %s", step.name, got, err, existing.ID)
				}
			}
		})
	})
}

func countUsers(ctx context.Context, t *testing.T, db *DB) int {
	t.Helper()
	n := 0
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	return n
}
