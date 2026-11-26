// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestEntrypointBindingsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialect(t, entrypointBindingsSQLiteVersion, entrypointBindingsPostgresVersion,
		func(t *testing.T, db *DB, version int64) {
			assertTableRoundTrips(t, db, version, "entrypoint_identity_bindings")
		})
}

func TestBindingRotatedAtMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialect(t, bindingRotatedAtSQLiteVersion, bindingRotatedAtPostgresVersion,
		func(t *testing.T, db *DB, version int64) {
			ctx := t.Context()
			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("migrate up to %d: %v", version, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(rotated_at) FROM entrypoint_identity_bindings`).Scan(new(int)); err != nil {
				t.Fatalf("rotated_at absent after migrate up: %v", err)
			}

			if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate down: %v", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entrypoint_identity_bindings`).Scan(new(int)); err != nil {
				t.Fatalf("the table went with the column; this version adds a column, not a table: %v", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(rotated_at) FROM entrypoint_identity_bindings`).Scan(new(int)); err == nil {
				t.Fatal("rotated_at survived the rollback; a re-apply then fails on a column that already exists")
			}

			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("re-apply after rollback: %v", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(rotated_at) FROM entrypoint_identity_bindings`).Scan(new(int)); err != nil {
				t.Fatalf("rotated_at absent after re-apply: %v", err)
			}
		})
}

func TestBindingRotatedAtMigration_ConvergesAnAlreadyMigratedDatabase(t *testing.T) {
	t.Parallel()
	forEachPlatformDialect(t, bindingRotatedAtSQLiteVersion, bindingRotatedAtPostgresVersion,
		func(t *testing.T, db *DB, version int64) {
			ctx := t.Context()
			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
				t.Fatalf("migrate up to %d: %v", version-1, err)
			}
			if _, err := db.ExecContext(ctx, `ALTER TABLE entrypoint_identity_bindings DROP COLUMN rotated_at`); err != nil {
				t.Logf("column already absent, which is the shape under test: %v", err)
			}

			if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
				t.Fatalf("the repair failed on the database it exists for: %v", err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(rotated_at) FROM entrypoint_identity_bindings`).Scan(new(int)); err != nil {
				t.Fatalf("rotated_at still absent after the repair; every login upsert still rolls back: %v", err)
			}
		})
}
