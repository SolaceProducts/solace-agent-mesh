// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"path/filepath"
	"testing"
)

func TestEvalMigration_ForwardAndRollback(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "eval_migration.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("forward: %v", err)
	}

	dRepo := NewEvalDatasetRepo(db)
	d := &EvalDataset{Name: "Smoke", CreatedBy: "u", UpdatedBy: "u"}
	if err := dRepo.Create(ctx, d); err != nil {
		t.Fatalf("post-migrate insert: %v", err)
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM eval_datasets`).Scan(&n); err != nil {
		t.Fatalf("count after re-forward: %v", err)
	}
	if n != 0 {
		t.Errorf("eval_datasets count = %d, want 0", n)
	}

	want := []string{
		"eval_datasets", "eval_examples", "eval_example_artifacts", "eval_evaluators",
		"eval_experiments", "eval_experiment_evaluators", "eval_runs", "eval_run_results",
		"eval_run_scores", "eval_agent_watchlist", "eval_tool_auth_attribution", "eval_tool_auth_agents",
		"eval_generation_operations",
	}
	for _, table := range want {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("missing table %s after re-forward: %v", table, err)
		}
	}
}
