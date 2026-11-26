// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const smartContextSavingsMigration = "smart_context_savings"

var savingsColumns = []struct{ table, column string }{
	{"token_spend_ledger", "avoided_input_tokens"},
	{"token_spend_ledger", "avoided_input_cost"},
	{"token_spend_daily", "avoided_input_tokens"},
	{"token_spend_daily", "avoided_input_cost"},
	{"token_spend_daily", "measured_calls"},
	{"token_spend_daily", "priced_measured_calls"},
	{"token_spend_daily_models", "avoided_input_tokens"},
	{"token_spend_daily_models", "avoided_input_cost"},
	{"token_spend_daily_models", "measured_calls"},
	{"token_spend_daily_models", "priced_measured_calls"},
}

func TestSmartContextSavingsMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, smartContextSavingsMigration, func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO token_spend_rollup_state (id, grain_version, refreshed_time, reconciled_time)
			VALUES (1, 2, 10, 20)`); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		for _, c := range savingsColumns {
			if !tableHasColumn(t, db, c.table, c.column) {
				t.Errorf("after up: %s.%s missing", c.table, c.column)
			}
		}
		state, err := NewSpendUsageRepo(db, nil).RollupState(ctx)
		if err != nil {
			t.Fatalf("rollup state: %v", err)
		}
		if state.RefreshedTime != nil || state.ReconciledTime != nil {
			t.Errorf("state after up = %+v, want both times cleared so the history reads as incomplete", state)
		}
		if grain, err := NewSpendRollupRepo(db).StoredGrainVersion(ctx); err != nil || grain != SpendRollupGrainVersion {
			t.Errorf("grain after up = %d, %v; want %d, so an older leader refuses the rollups rather than rebuild a day without the new sums", grain, err, SpendRollupGrainVersion)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, c := range savingsColumns {
			if tableHasColumn(t, db, c.table, c.column) {
				t.Errorf("after down: %s.%s survived the rollback", c.table, c.column)
			}
		}
		if grain, err := NewSpendRollupRepo(db).StoredGrainVersion(ctx); err != nil || grain != 2 {
			t.Errorf("grain after down = %d, %v; want 2, the previous release's own", grain, err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
	})
}
