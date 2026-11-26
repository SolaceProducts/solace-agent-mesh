// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestAPIKeysMigration_TablesRoundTrip(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"api_keys", "api_key_scopes"} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()
			forEachPlatformDialectAt(t, "api_keys", func(t *testing.T, db *DB, version int64) {
				assertTableRoundTrips(t, db, version, table)
			})
		})
	}
}

func TestAPIKeysMigration_KeyHashIsUnique(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "api_keys", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		const ownerID = "11111111-1111-1111-1111-111111111111"
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users (id, issuer, sub, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ownerID, "sam:system", "system:uniq", "test", "test", 1, 1); err != nil {
			t.Fatalf("seed owner: %v", err)
		}

		insert := func(id string) error {
			_, err := db.ExecContext(ctx,
				`INSERT INTO api_keys
				 (id, user_id, label, key_hash, prefix, created_by, updated_by, created_time, updated_time)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, ownerID, "dup", "the-same-digest", "abcd1234", "test", "test", 1, 1)
			return err
		}
		if err := insert("22222222-2222-2222-2222-222222222222"); err != nil {
			t.Fatalf("first insert: %v", err)
		}
		err := insert("33333333-3333-3333-3333-333333333333")
		if err == nil {
			t.Fatal("a second key with the same key_hash was accepted; ux_api_keys_key_hash is missing")
		}
		if !dbutil.IsUniqueViolation(err) {
			t.Errorf("duplicate key_hash failed with %v; want a unique violation", err)
		}
	})
}

func TestAPIKeysMigration_OwnerCascadeIsDeclaredInTheSchema(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "api_keys", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		const ownerID = "44444444-4444-4444-4444-444444444444"
		const keyID = "55555555-5555-5555-5555-555555555555"
		if _, err := db.ExecContext(ctx,
			`INSERT INTO users (id, issuer, sub, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ownerID, "sam:system", "system:cascade", "test", "test", 1, 1); err != nil {
			t.Fatalf("seed owner: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO api_keys
			 (id, user_id, label, key_hash, prefix, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			keyID, ownerID, "k", "cascade-digest", "abcd1234", "test", "test", 1, 1); err != nil {
			t.Fatalf("seed key: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO api_key_scopes (api_key_id, scope) VALUES (?, ?)`,
			keyID, "agent:*:invoke"); err != nil {
			t.Fatalf("seed scope: %v", err)
		}

		if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, ownerID); err != nil {
			t.Fatalf("delete owner: %v", err)
		}
		var keys, scopes int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&keys); err != nil {
			t.Fatalf("count keys: %v", err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_key_scopes`).Scan(&scopes); err != nil {
			t.Fatalf("count scopes: %v", err)
		}
		if keys != 0 {
			t.Errorf("%d api_keys rows survived their owner; the credential would still authenticate", keys)
		}
		if scopes != 0 {
			t.Errorf("%d api_key_scopes rows survived, orphaned", scopes)
		}
	})
}
