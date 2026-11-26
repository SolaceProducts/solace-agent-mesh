// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestUsersMigration_AppliesAndIsReversible(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "users.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 66); err != nil {
		t.Fatalf("migrate up to 66: %v", err)
	}
	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 67); err != nil {
		t.Fatalf("migrate up to 67: %v", err)
	}

	rawInsert := func(id, sub string, phone any) error {
		_, e := db.RawDB().ExecContext(ctx,
			`INSERT INTO users (id, issuer, sub, phone, source, provisioned_via, active, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, '{}', 'jit', 1, 'x', 'x', 1, 1)`,
			id, "iss", sub, phone)
		return e
	}

	if err := rawInsert("u1", "s1", nil); err != nil {
		t.Fatalf("insert u1: %v", err)
	}
	if err := rawInsert("u2", "s2", nil); err != nil {
		t.Fatalf("two NULL phones must coexist: %v", err)
	}
	if err := rawInsert("u3", "s3", "+15550000001"); err != nil {
		t.Fatalf("insert u3: %v", err)
	}
	if err := rawInsert("u4", "s4", "+15550000001"); !dbutil.IsUniqueViolation(err) {
		t.Fatalf("duplicate phone: want unique-constraint violation, got %v", err)
	}

	if err := db.MigrateDown(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-forward: %v", err)
	}
	n := 0
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count after down+up: %v", err)
	}
	if n != seededSystemUserCount {
		t.Errorf("users count after down+up = %d, want %d (the seeded system users)", n, seededSystemUserCount)
	}
}
