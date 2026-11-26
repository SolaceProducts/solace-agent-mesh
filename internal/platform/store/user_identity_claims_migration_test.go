// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const userIdentityClaimsSQLiteVersion = 82

func TestUserIdentityClaimsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialect(t, userIdentityClaimsSQLiteVersion, userIdentityClaimsPostgresVersion,
		func(t *testing.T, db *DB, version int64) {
			assertTableRoundTrips(t, db, version, "user_identity_claims")
		})
}

func assertTableRoundTrips(t *testing.T, db *DB, version int64, table string) {
	t.Helper()
	ctx := t.Context()
	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(new(int)); err != nil {
		t.Fatalf("table absent after migrate up: %v", err)
	}

	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(new(int)); err == nil {
		t.Fatal("querying the dropped table succeeded; the -- +goose Down is incomplete")
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(new(int)); err != nil {
		t.Fatalf("table absent after re-apply: %v", err)
	}
}

func TestUserIdentityClaimsMigration_LookupIndexIsNotUnique(t *testing.T) {
	t.Parallel()
	forEachPlatformDialect(t, userIdentityClaimsSQLiteVersion, userIdentityClaimsPostgresVersion, assertLookupIndexIsNotUnique)
}

func assertLookupIndexIsNotUnique(t *testing.T, db *DB, version int64) {
	t.Helper()
	ctx := migrateClaimsUpTo(t, db, version)
	first := seedClaimsUser(t, db, "issuer-a", "sub-a")
	second := seedClaimsUser(t, db, "issuer-b", "sub-b")

	const recycled = "15551234567"
	for _, id := range []string{first, second} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO user_identity_claims (user_id, claim_key, claim_value_canonical, verified, updated_time)
			 VALUES (?, 'phone_number', ?, 1, 1)`, id, recycled); err != nil {
			t.Fatalf("insert claim for %s: %v", id, err)
		}
	}

	ids, err := dbutil.QueryVals[string](ctx, db,
		`SELECT user_id FROM user_identity_claims WHERE claim_key = 'phone_number' AND claim_value_canonical = ? ORDER BY user_id`, recycled)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("holders of a recycled value = %d (%v), want 2 — a unique index would have dropped one and resolved the value to the wrong user", len(ids), ids)
	}
}

func TestUserIdentityClaimsMigration_CascadesOnUserDelete(t *testing.T) {
	t.Parallel()
	forEachPlatformDialect(t, userIdentityClaimsSQLiteVersion, userIdentityClaimsPostgresVersion, assertCascadesOnUserDelete)
}

func assertCascadesOnUserDelete(t *testing.T, db *DB, version int64) {
	t.Helper()
	ctx := migrateClaimsUpTo(t, db, version)
	userID := seedClaimsUser(t, db, "issuer-c", "sub-c")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO user_identity_claims (user_id, claim_key, claim_value_canonical, verified, updated_time)
		 VALUES (?, 'phone_number', '15550000000', 1, 1)`, userID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	remaining, err := dbutil.QueryVal[int](ctx, db,
		`SELECT COUNT(*) FROM user_identity_claims WHERE user_id = ?`, userID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("claim rows surviving the user = %d, want 0 — a deleted user's value must stop matching", remaining)
	}
}

func migrateClaimsUpTo(t *testing.T, db *DB, version int64) context.Context {
	t.Helper()
	ctx := t.Context()
	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	return ctx
}

func seedClaimsUser(t *testing.T, db *DB, issuer, sub string) string {
	t.Helper()
	id := idgen.New()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO users (id, issuer, sub, source, provisioned_via, active, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, '{}', 'jit', 1, 'test', 'test', 1, 1)`, id, issuer, sub); err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
	return id
}
