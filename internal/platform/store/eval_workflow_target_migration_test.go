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

const evalWorkflowTargetsSQLiteVersion = 115

func TestEvalWorkflowTargetsMigration_RoundTrip(t *testing.T) {
	t.Parallel()

	db, openErr := Open("sqlite:///"+filepath.Join(t.TempDir(), "eval_workflow_targets.db"), nil)
	if openErr != nil {
		t.Fatalf("open: %v", openErr)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()

	columns := map[string][]string{
		"eval_experiments": {"target_type"},
		"eval_examples":    {"input_data"},
		"eval_run_results": {"target_type", "token_breakdown"},
	}
	assertColumns := func(want bool, when string) {
		t.Helper()
		for table, cols := range columns {
			for _, col := range cols {
				if got := columnExists(t, db, table, col); got != want {
					t.Errorf("%s: %s.%s present = %v, want %v", when, table, col, got, want)
				}
			}
		}
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), evalWorkflowTargetsSQLiteVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", evalWorkflowTargetsSQLiteVersion-1, err)
	}
	assertColumns(false, "before the migration")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_experiments (id, name, target_agent, models, runs_per_example, max_workers,
		  is_default, created_by, updated_by, created_time, updated_time)
		 VALUES ('exp-1', 'legacy', 'Orchestrator', '[]', 1, 4, 0, 'u', 'u', 1, 1)`); err != nil {
		t.Fatalf("seed pre-migration experiment: %v", err)
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), evalWorkflowTargetsSQLiteVersion); err != nil {
		t.Fatalf("migrate up to %d: %v", evalWorkflowTargetsSQLiteVersion, err)
	}
	assertColumns(true, "after the migration")
	legacy, err := NewEvalExperimentRepo(db).GetByID(ctx, "exp-1")
	if err != nil {
		t.Fatalf("read pre-migration experiment: %v", err)
	}
	if legacy.TargetType != EvalTargetTypeAgent || legacy.IsWorkflowTarget() {
		t.Errorf("pre-migration experiment target type = %q, want %q", legacy.TargetType, EvalTargetTypeAgent)
	}

	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), evalWorkflowTargetsSQLiteVersion-1); err != nil {
		t.Fatalf("roll back: %v", err)
	}
	assertColumns(false, "after rollback")

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), evalWorkflowTargetsSQLiteVersion); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	assertColumns(true, "after re-apply")
}

func TestEvalExperimentRepo_TargetTypeRoundTrips(t *testing.T) {
	db := TestDB(t)
	repo := NewEvalExperimentRepo(db)
	ctx := t.Context()

	agentExp := &EvalExperiment{Name: "agent-exp", TargetAgent: "OrderDesk", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, agentExp, nil); err != nil {
		t.Fatalf("create agent experiment: %v", err)
	}
	workflowExp := &EvalExperiment{Name: "wf-exp", TargetAgent: "OrderDesk", TargetType: EvalTargetTypeWorkflow, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, workflowExp, nil); err != nil {
		t.Fatalf("create workflow experiment: %v", err)
	}

	got, err := repo.GetByID(ctx, agentExp.ID)
	if err != nil {
		t.Fatalf("get agent experiment: %v", err)
	}
	if got.TargetType != EvalTargetTypeAgent {
		t.Errorf("unset target type stored as %q, want %q", got.TargetType, EvalTargetTypeAgent)
	}
	got, err = repo.GetByID(ctx, workflowExp.ID)
	if err != nil {
		t.Fatalf("get workflow experiment: %v", err)
	}
	if !got.IsWorkflowTarget() {
		t.Errorf("workflow target type stored as %q", got.TargetType)
	}

	listed, err := repo.List(ctx, "", "", "OrderDesk")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != agentExp.ID {
		t.Errorf("filtering by agent OrderDesk listed %d experiments, want only the agent one", len(listed))
	}

	agentType := EvalTargetTypeAgent
	updated, err := repo.Update(ctx, workflowExp.ID, EvalExperimentUpdate{TargetType: &agentType, UpdatedBy: "u"})
	if err != nil {
		t.Fatalf("retarget: %v", err)
	}
	if updated.IsWorkflowTarget() {
		t.Error("update did not change the target type")
	}
}
