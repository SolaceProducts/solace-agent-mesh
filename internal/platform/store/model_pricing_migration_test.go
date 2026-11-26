// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var priceColumns = []string{"price_input", "price_output", "price_cache_read", "price_cache_write"}

const existingModelID = "0199a000-0000-7000-8000-000000000001"

func TestModelPricingMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "model_pricing", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if _, err := db.ExecContext(ctx, db.Rebind(`INSERT INTO model_configurations
			(id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
			VALUES (?, 'existing', 'openai', 'gpt', 'test', 'test', 0, 0)`), existingModelID); err != nil {
			t.Fatalf("insert a model before the migration: %v", err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		for _, column := range priceColumns {
			if !hasModelConfigColumn(t, db, column) {
				t.Fatalf("%s absent after migrate up", column)
			}
		}
		assertExistingModelUnpriced(t, db)

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		for _, column := range priceColumns {
			if hasModelConfigColumn(t, db, column) {
				t.Errorf("%s survived the rollback; a re-apply then finds a column that already exists", column)
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		for _, column := range priceColumns {
			if !hasModelConfigColumn(t, db, column) {
				t.Fatalf("%s absent after re-apply", column)
			}
		}
		if db.Dialect() == "postgres" {
			assertPostgresRefusesHalfPairs(t, db)
		}
	})
}

func assertExistingModelUnpriced(t *testing.T, db *DB) {
	t.Helper()
	nulls, err := dbutil.QueryVal[int](t.Context(), db, db.Rebind(`SELECT COUNT(*) FROM model_configurations
		WHERE id = ? AND price_input IS NULL AND price_output IS NULL
		AND price_cache_read IS NULL AND price_cache_write IS NULL`), existingModelID)
	if err != nil {
		t.Fatalf("read the pre-existing model: %v", err)
	}
	if nulls != 1 {
		t.Error("a model that existed before the migration did not come out with every rate null")
	}
}

func assertPostgresRefusesHalfPairs(t *testing.T, db *DB) {
	t.Helper()
	for name, set := range map[string]string{
		"input without output":     "price_input = 1",
		"cache read without write": "price_input = 1, price_output = 1, price_cache_read = 1",
		"cache without the pair":   "price_cache_read = 1, price_cache_write = 1",
	} {
		_, err := db.ExecContext(t.Context(), db.Rebind(`UPDATE model_configurations SET `+set+` WHERE id = ?`), existingModelID)
		if err == nil || !strings.Contains(err.Error(), "check constraint") {
			t.Errorf("%s: err = %v, want a check-constraint violation", name, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), db.Rebind(`UPDATE model_configurations
		SET price_input = 1, price_output = 1, price_cache_read = 1, price_cache_write = 1 WHERE id = ?`), existingModelID); err != nil {
		t.Errorf("a complete price was refused: %v", err)
	}
}

func hasModelConfigColumn(t *testing.T, db *DB, column string) bool {
	t.Helper()
	query := `SELECT COUNT(*) FROM pragma_table_info('model_configurations') WHERE name = ?`
	if db.Dialect() == "postgres" {
		query = `SELECT COUNT(*) FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'model_configurations' AND column_name = ?`
	}
	count, err := dbutil.QueryVal[int](t.Context(), db, db.Rebind(query), column)
	if err != nil {
		t.Fatalf("look up column %s: %v", column, err)
	}
	return count > 0
}
