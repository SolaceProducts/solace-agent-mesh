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

func TestSkillReferenceAssetMigration_Backfill(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "skillsplit.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 61); err != nil {
		t.Fatalf("migrate up to 61: %v", err)
	}

	seeded := []struct {
		id            string
		name          string
		hasRefs       int
		hasAssets     int
		resourceCount int
		wantRef       int
		wantAsset     int
	}{
		{"a1111111-1111-1111-1111-111111111111", "refs-only", 1, 0, 4, 4, 0},
		{"a2222222-2222-2222-2222-222222222222", "assets-only", 0, 1, 6, 0, 6},
		{"a3333333-3333-3333-3333-333333333333", "mixed", 1, 1, 9, 0, 0},
	}
	for _, s := range seeded {
		_, err := db.RawDB().ExecContext(ctx,
			`INSERT INTO skills (id, name, origin, discovery_status, has_references, has_assets, has_tools, resource_count, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.id, s.name, "platform", "ready", s.hasRefs, s.hasAssets, 0, s.resourceCount, "test", "test", int64(1), int64(1),
		)
		if err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 62); err != nil {
		t.Fatalf("migrate up to 62: %v", err)
	}

	for _, s := range seeded {
		var ref, asset, total int
		err := db.QueryRowContext(ctx,
			`SELECT reference_count, asset_count, resource_count FROM skills WHERE id = ?`, s.id,
		).Scan(&ref, &asset, &total)
		if err != nil {
			t.Fatalf("select counts for %s: %v", s.name, err)
		}
		if ref != s.wantRef {
			t.Errorf("%s: reference_count = %d, want %d", s.name, ref, s.wantRef)
		}
		if asset != s.wantAsset {
			t.Errorf("%s: asset_count = %d, want %d", s.name, asset, s.wantAsset)
		}
		if total != s.resourceCount {
			t.Errorf("%s: resource_count = %d, want %d (unchanged)", s.name, total, s.resourceCount)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("forward (idempotent): %v", err)
	}
	if err := db.MigrateDown(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-forward: %v", err)
	}
	n := 0
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills`).Scan(&n); err != nil {
		t.Fatalf("count after re-forward: %v", err)
	}
	if n != 0 {
		t.Errorf("skills count after down+up = %d, want 0", n)
	}
}
