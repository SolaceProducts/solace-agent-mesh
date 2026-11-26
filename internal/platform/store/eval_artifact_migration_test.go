// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const evalExampleArtifactsSQLiteVersion = 77

func TestEvalExampleArtifactsMigration_RoundTrip(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "eval_example_artifacts.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	resultColumns := []string{"input_artifact_metadata", "expected_artifact_metadata"}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), evalExampleArtifactsSQLiteVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", evalExampleArtifactsSQLiteVersion-1, err)
	}
	if tableExists(t, db, "eval_example_artifacts") {
		t.Error("eval_example_artifacts exists before the migration")
	}
	for _, col := range resultColumns {
		if columnExists(t, db, "eval_run_results", col) {
			t.Errorf("eval_run_results.%s exists before the migration", col)
		}
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), evalExampleArtifactsSQLiteVersion); err != nil {
		t.Fatalf("migrate up to %d: %v", evalExampleArtifactsSQLiteVersion, err)
	}
	if !tableExists(t, db, "eval_example_artifacts") {
		t.Error("eval_example_artifacts missing after the migration")
	}
	for _, col := range resultColumns {
		if !columnExists(t, db, "eval_run_results", col) {
			t.Errorf("eval_run_results.%s missing after the migration", col)
		}
	}

	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), evalExampleArtifactsSQLiteVersion-1); err != nil {
		t.Fatalf("migrate down past the artifact table: %v", err)
	}
	if tableExists(t, db, "eval_example_artifacts") {
		t.Error("eval_example_artifacts survived rollback")
	}
	for _, col := range resultColumns {
		if columnExists(t, db, "eval_run_results", col) {
			t.Errorf("eval_run_results.%s survived rollback", col)
		}
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), evalExampleArtifactsSQLiteVersion); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	if !tableExists(t, db, "eval_example_artifacts") {
		t.Error("eval_example_artifacts missing after re-apply")
	}
	for _, col := range resultColumns {
		if !columnExists(t, db, "eval_run_results", col) {
			t.Errorf("eval_run_results.%s missing after re-apply", col)
		}
	}
}

func tableExists(t *testing.T, db *DB, table string) bool {
	t.Helper()
	count, err := dbutil.QueryVal[int](t.Context(), db,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table)
	if err != nil {
		t.Fatalf("sqlite_master(%s): %v", table, err)
	}
	return count > 0
}

func columnExists(t *testing.T, db *DB, table, column string) bool {
	t.Helper()
	count, err := dbutil.QueryVal[int](t.Context(), db,
		fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`, table), column)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	return count > 0
}
