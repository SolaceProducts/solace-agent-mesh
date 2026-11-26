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

func TestGatewaySlugMigration_BackfillsAndIsReversible(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "slug.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 49); err != nil {
		t.Fatalf("migrate up to 49: %v", err)
	}

	seeded := []struct {
		id   string
		name string
	}{
		{"11111111-1111-1111-1111-111111111111", "alpha"},
		{"22222222-2222-2222-2222-222222222222", "beta"},
		{"33333333-3333-3333-3333-333333333333", "gamma"},
	}
	for _, s := range seeded {
		_, err := db.RawDB().ExecContext(ctx,
			`INSERT INTO gateways (id, name, type, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			s.id, s.name, "http_api", "test", "test", int64(1), int64(1),
		)
		if err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), 50); err != nil {
		t.Fatalf("migrate up to 50: %v", err)
	}

	for _, s := range seeded {
		var slug string
		err := db.QueryRowContext(ctx,
			`SELECT slug FROM gateways WHERE id = ?`, s.id,
		).Scan(&slug)
		if err != nil {
			t.Fatalf("select slug for %s: %v", s.id, err)
		}
		if slug != s.id {
			t.Errorf("gateway %s: slug = %q, want %q", s.id, slug, s.id)
		}
	}

	_, dupErr := db.RawDB().ExecContext(ctx,
		`INSERT INTO gateways (id, name, type, slug, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"44444444-4444-4444-4444-444444444444", "alpha-dup", "http_api",
		seeded[0].id, "test", "test", int64(1), int64(1),
	)
	if dupErr == nil {
		t.Fatalf("duplicate slug insert: expected unique-constraint error, got nil")
	}
	if !dbutil.IsUniqueViolation(dupErr) {
		t.Fatalf("duplicate slug insert: expected unique-constraint violation, got %v", dupErr)
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gateways`).Scan(&n); err != nil {
		t.Fatalf("count after re-forward: %v", err)
	}
	if n != 0 {
		t.Errorf("gateways count after down+up = %d, want 0", n)
	}
}
