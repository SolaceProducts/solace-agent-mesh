// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const workflowRollupsMigration = "token_spend_rollups_by_workflow"

var workflowRollupIndexes = []string{
	"idx_token_spend_daily_workflow",
	"idx_token_spend_daily_workflow_name",
	"idx_token_spend_daily_activities_workflow",
	"idx_token_spend_daily_activities_workflow_name",
}

func TestSpendRollupsByWorkflowMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, workflowRollupsMigration, func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO token_spend_rollup_state (id, grain_version, refreshed_time, reconciled_time)
			VALUES (1, 1, 10, 20)`); err != nil {
			t.Fatalf("seed state: %v", err)
		}
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		for _, name := range append(spendRollupSchemaObjects, workflowRollupIndexes...) {
			if !schemaObjectExists(t, db, name) {
				t.Errorf("after up: %s missing", name)
			}
		}
		state, err := NewSpendUsageRepo(db, nil).RollupState(ctx)
		if err != nil {
			t.Fatalf("rollup state: %v", err)
		}
		if state.RefreshedTime != nil || state.ReconciledTime != nil {
			t.Errorf("state after up = %+v, want both times cleared so the history reads as incomplete", state)
		}
		if grain, err := NewSpendRollupRepo(db).StoredGrainVersion(ctx); err != nil || grain != 2 {
			t.Errorf("grain after up = %d, %v; want 2, so an older leader refuses the new tables from the start", grain, err)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, name := range workflowRollupIndexes {
			if schemaObjectExists(t, db, name) {
				t.Errorf("after down: %s survived the rollback", name)
			}
		}
		for _, name := range spendRollupSchemaObjects {
			if !schemaObjectExists(t, db, name) {
				t.Errorf("after down: %s missing", name)
			}
		}
		if n, err := dbutil.QueryVal[int](ctx, db, `SELECT COUNT(*) FROM token_spend_rollup_state`); err != nil || n != 0 {
			t.Errorf("state rows after down = %d, %v; want none", n, err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
	})
}

func TestSpendRollupsByWorkflowMigration_Constraints(t *testing.T) {
	t.Parallel()
	const (
		userRow     = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77"
		workflowRow = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e78"
		utcDay      = int64(20_000) * 86_400_000
	)
	atWorkflowRollups := templatesMigratedTo(t, workflowRollupsMigration)
	for name, tc := range map[string]struct {
		insert         rollupFixture
		wantConstraint string
	}{
		"a workflow activity row by id":          {insert: activityRow("workflow", map[string]any{"user_id": userRow, "workflow_id": workflowRow})},
		"a workflow activity row by name":        {insert: activityRow("workflow", map[string]any{"user_sub": "alice", "workflow_name": "nightly"})},
		"an older release's root activity row":   {insert: activityRow("root", map[string]any{"user_id": userRow, "root_kind": "workflow", "root_id": workflowRow})},
		"a daily row with a workflow label":      {insert: dailyRow(map[string]any{"user_id": userRow, "workflow_id": workflowRow, "workflow_label": "Nightly", "model_name": "gpt-x"})},
		"an older release's daily row, no label": {insert: dailyRow(map[string]any{"user_id": userRow, "workflow_name": "nightly", "model_name": "gpt-x"})},

		"a workflow activity row with no workflow":     {insert: activityRow("workflow", map[string]any{"user_id": userRow}), wantConstraint: "token_spend_daily_activities_workflow"},
		"a workflow activity row with both sides":      {insert: activityRow("workflow", map[string]any{"user_id": userRow, "workflow_id": workflowRow, "workflow_name": "nightly"}), wantConstraint: "token_spend_daily_activities_workflow"},
		"a workflow on a root activity row":            {insert: activityRow("root", map[string]any{"user_id": userRow, "root_kind": "agent", "root_name": "qa", "workflow_name": "nightly"}), wantConstraint: "token_spend_daily_activities_workflow"},
		"a root on a workflow activity row":            {insert: activityRow("workflow", map[string]any{"user_id": userRow, "workflow_name": "nightly", "root_kind": "workflow", "root_name": "nightly"}), wantConstraint: "token_spend_daily_activities_root"},
		"an agent on a workflow activity row":          {insert: activityRow("workflow", map[string]any{"user_id": userRow, "workflow_name": "nightly", "agent_name": "qa"}), wantConstraint: "token_spend_daily_activities_agent"},
		"a grouping neither release writes is refused": {insert: activityRow("tool", map[string]any{"user_id": userRow}), wantConstraint: "token_spend_daily_activities_grouping"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			forEachPlatformDialectClone(t, atWorkflowRollups, func(t *testing.T, db *DB) {
				query, args := tc.insert.sql(utcDay)
				_, err := db.ExecContext(t.Context(), db.Rebind(query), args...)
				switch {
				case tc.wantConstraint == "" && err != nil:
					t.Fatalf("insert refused: %v", err)
				case tc.wantConstraint != "" && err == nil:
					t.Fatalf("insert accepted, want %s to refuse it", tc.wantConstraint)
				case tc.wantConstraint != "" && !namesConstraint(err, tc.wantConstraint):
					t.Fatalf("insert refused by %v, want %s", err, tc.wantConstraint)
				}
			})
		})
	}
}

func TestSpendRollupsByWorkflowMigration_PostgresKeepsTheFillfactor(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, workflowRollupsMigration, func(t *testing.T, db *DB, version int64) {
		if db.Dialect() != "postgres" {
			return
		}
		if err := dbutil.MigrateUpTo(t.Context(), db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		requireFillfactor80(t, db, "token_spend_daily", "token_spend_daily_activities")
	})
}
