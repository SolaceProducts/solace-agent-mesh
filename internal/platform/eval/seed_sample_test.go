// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type sampleSeedTestEnv struct {
	deps           sampleSeedDeps
	db             *store.DB
	datasetRepo    *store.EvalDatasetRepo
	exampleRepo    *store.EvalExampleRepo
	experimentRepo *store.EvalExperimentRepo
	evaluatorRepo  *store.EvalEvaluatorRepo
}

func newSampleSeedTestEnv(t *testing.T) *sampleSeedTestEnv {
	t.Helper()
	db := store.TestDB(t)
	evaluatorRepo := store.NewEvalEvaluatorRepo(db)
	if err := SeedBuiltinEvaluators(t.Context(), evaluatorRepo, nil); err != nil {
		t.Fatalf("seed builtins: %v", err)
	}
	return &sampleSeedTestEnv{
		deps:           NewSampleSeedDeps(db, evaluatorRepo),
		db:             db,
		datasetRepo:    store.NewEvalDatasetRepo(db),
		exampleRepo:    store.NewEvalExampleRepo(db),
		experimentRepo: store.NewEvalExperimentRepo(db),
		evaluatorRepo:  evaluatorRepo,
	}
}

func TestSeedSampleExperimentCreatesDefaultRows(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed sample: %v", err)
	}

	exp, err := env.experimentRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault experiment: %v", err)
	}
	if exp.Name != sampleExperimentName {
		t.Errorf("experiment name = %q, want %q", exp.Name, sampleExperimentName)
	}
	if !exp.IsDefault {
		t.Error("experiment IsDefault must be true")
	}
	if exp.TargetAgent != sampleTargetAgent {
		t.Errorf("target agent = %q, want %q", exp.TargetAgent, sampleTargetAgent)
	}
	if exp.DatasetID == nil {
		t.Fatal("experiment must reference the seeded dataset")
	}

	ds, err := env.datasetRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("GetDefault dataset: %v", err)
	}
	if ds.ID != *exp.DatasetID {
		t.Errorf("default dataset id mismatch: %q vs experiment.DatasetID %q", ds.ID, *exp.DatasetID)
	}
	if !ds.IsDefault {
		t.Error("dataset IsDefault must be true")
	}

	examples, err := env.exampleRepo.ListByDataset(ctx, ds.ID)
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	if len(examples) != len(sampleExamples) {
		t.Errorf("example count = %d, want %d", len(examples), len(sampleExamples))
	}

	evIDs, err := env.experimentRepo.GetEvaluatorIDs(ctx, exp.ID)
	if err != nil {
		t.Fatalf("GetEvaluatorIDs: %v", err)
	}
	if len(evIDs) != 1 {
		t.Errorf("attached evaluators = %d, want 1", len(evIDs))
	}
}

func TestSeedSampleExperimentIdempotent(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	first, err := env.experimentRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("get first: %v", err)
	}

	if err = SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed second: %v", err)
	}
	second, err := env.experimentRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("seed not idempotent: first id=%q, second id=%q", first.ID, second.ID)
	}
}

func TestSeedSampleExperimentSkipsAfterExperimentDeleted(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	exp, err := env.experimentRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := env.experimentRepo.Delete(ctx, exp.ID, nil); err != nil {
		t.Fatalf("delete experiment: %v", err)
	}

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("re-seed after delete should be a no-op, got: %v", err)
	}

	if _, err := env.experimentRepo.GetDefault(ctx); err == nil {
		t.Error("seed re-created the deleted sample experiment; operator deletion not respected")
	}

	if _, err := env.datasetRepo.GetDefault(ctx); err != nil {
		t.Errorf("default dataset should persist after experiment deletion; got %v", err)
	}
}

func TestSeedSampleExperimentSkipsAfterFullDeletionViaMarker(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	exp, err := env.experimentRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("get experiment: %v", err)
	}
	ds, err := env.datasetRepo.GetDefault(ctx)
	if err != nil {
		t.Fatalf("get dataset: %v", err)
	}

	if err := env.experimentRepo.Delete(ctx, exp.ID, nil); err != nil {
		t.Fatalf("delete experiment: %v", err)
	}
	if err := env.datasetRepo.Delete(ctx, ds.ID, nil); err != nil {
		t.Fatalf("delete dataset: %v", err)
	}

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("re-seed after full deletion must be a no-op, got: %v", err)
	}

	if _, err := env.experimentRepo.GetDefault(ctx); err == nil {
		t.Error("marker failed: seed re-created the deleted experiment")
	}
	if _, err := env.datasetRepo.GetDefault(ctx); err == nil {
		t.Error("marker failed: seed re-created the deleted dataset")
	}
}

func TestSeedSampleExperimentBackfillsMarkerOnUpgrade(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	now := time.Now().UnixMilli()
	datasetID := idgen.New()
	experimentID := idgen.New()
	if _, err := env.db.ExecContext(ctx,
		`INSERT INTO eval_datasets (id, name, description, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, 1, 'system', 'system', ?, ?)`,
		datasetID, sampleDatasetName, sampleDatasetDesc, now, now,
	); err != nil {
		t.Fatalf("setup: insert dataset: %v", err)
	}
	ev, err := env.evaluatorRepo.GetByName(ctx, sampleEvaluatorName)
	if err != nil {
		t.Fatalf("setup: get evaluator: %v", err)
	}
	if _, err := env.db.ExecContext(ctx,
		`INSERT INTO eval_experiments
		 (id, name, description, dataset_id, target_agent, models, runs_per_example, max_workers,
		  primary_evaluator_id, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, 1, 4, ?, 1, 'system', 'system', ?, ?)`,
		experimentID, sampleExperimentName, sampleExperimentDesc, datasetID,
		sampleTargetAgent, sampleModelsJSON, ev.ID, now, now,
	); err != nil {
		t.Fatalf("setup: insert experiment: %v", err)
	}

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed (upgrade backfill): %v", err)
	}
	var count int
	if err := env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM system_seed_state WHERE seed_key = ?`, sampleSeedMarkerKey,
	).Scan(&count); err != nil {
		t.Fatalf("probe marker: %v", err)
	}
	if count != 1 {
		t.Fatalf("marker not backfilled on upgrade boot: count = %d, want 1", count)
	}

	if err := env.experimentRepo.Delete(ctx, experimentID, nil); err != nil {
		t.Fatalf("delete experiment: %v", err)
	}
	if err := env.datasetRepo.Delete(ctx, datasetID, nil); err != nil {
		t.Fatalf("delete dataset: %v", err)
	}
	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("re-seed after upgrade-backfill + delete: %v", err)
	}
	if _, err := env.experimentRepo.GetDefault(ctx); err == nil {
		t.Error("seed re-created experiment despite marker; operator deletion not respected")
	}
}

func TestSeedSampleExperimentTertiaryGateOrphanDataset(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	now := time.Now().UnixMilli()
	orphanDatasetID := idgen.New()
	const orphanName = "Operator Default Dataset"
	if _, err := env.db.ExecContext(ctx,
		`INSERT INTO eval_datasets (id, name, description, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, 1, 'system', 'system', ?, ?)`,
		orphanDatasetID, orphanName, "pre-fix orphan", now, now,
	); err != nil {
		t.Fatalf("setup: insert orphan dataset: %v", err)
	}

	if err := SeedSampleExperiment(ctx, env.deps, nil); err != nil {
		t.Fatalf("seed (tertiary gate): %v", err)
	}

	var markerCount int
	if err := env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM system_seed_state WHERE seed_key = ?`, sampleSeedMarkerKey,
	).Scan(&markerCount); err != nil {
		t.Fatalf("probe marker: %v", err)
	}
	if markerCount != 1 {
		t.Errorf("marker count = %d, want 1 (tertiary gate must set the marker)", markerCount)
	}

	preserved, err := env.datasetRepo.GetByID(ctx, orphanDatasetID)
	if err != nil {
		t.Fatalf("orphan dataset lookup: %v", err)
	}
	if preserved == nil || preserved.Name != orphanName {
		t.Errorf("orphan dataset was modified or removed; got %v", preserved)
	}

	if _, err := env.experimentRepo.GetDefault(ctx); err == nil {
		t.Error("tertiary gate created an experiment; should be a marker-only path")
	}
}

func TestSeedSampleExperimentRollsBackOnMidTxFailure(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()

	now := time.Now().UnixMilli()
	preExperimentID := idgen.New()
	if _, err := env.db.ExecContext(ctx,
		`INSERT INTO eval_experiments
		 (id, name, description, target_agent, models, runs_per_example, max_workers,
		  is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, '[]', 1, 4, 0, 'test', 'test', ?, ?)`,
		preExperimentID, sampleExperimentName, "pre-existing collision row", "SomeAgent", now, now,
	); err != nil {
		t.Fatalf("setup: pre-insert colliding experiment: %v", err)
	}

	err := SeedSampleExperiment(ctx, env.deps, nil)
	if err == nil {
		t.Fatal("expected seed to fail on UNIQUE(name) collision with pre-seeded experiment")
	}

	dsRows, err := env.datasetRepo.List(ctx)
	if err != nil {
		t.Fatalf("list datasets: %v", err)
	}
	for _, ds := range dsRows {
		if ds.IsDefault {
			t.Errorf("rollback failed: default dataset %s persisted after seed failure", ds.ID)
		}
		if ds.Name == sampleDatasetName {
			t.Errorf("rollback failed: dataset %q persisted after seed failure", ds.Name)
		}
	}

	var orphanExamples int
	if err = env.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM eval_examples e
		 LEFT JOIN eval_datasets d ON d.id = e.dataset_id
		 WHERE d.id IS NULL`,
	).Scan(&orphanExamples); err != nil {
		t.Fatalf("count orphan examples: %v", err)
	}
	if orphanExamples != 0 {
		t.Errorf("rollback failed: %d orphan example rows survived", orphanExamples)
	}

	pre, err := env.experimentRepo.GetByID(ctx, preExperimentID)
	if err != nil {
		t.Fatalf("pre-seeded experiment vanished after rollback: %v", err)
	}
	if pre.Name != sampleExperimentName {
		t.Errorf("pre-seeded experiment name = %q, want %q", pre.Name, sampleExperimentName)
	}
}

func TestSettleSampleSeedAcceptsAFailureAnotherInstanceSeeded(t *testing.T) {
	env := newSampleSeedTestEnv(t)
	ctx := t.Context()
	lost := errors.New("insert dataset: UNIQUE constraint failed")

	if err := settleSampleSeed(ctx, env.db, lost, slog.Default()); !errors.Is(err, lost) {
		t.Fatalf("without a marker: err = %v, want %v", err, lost)
	}

	if _, err := env.db.ExecContext(ctx,
		`INSERT INTO system_seed_state (seed_key, seeded_at) VALUES (?, ?)`,
		sampleSeedMarkerKey, time.Now().UnixMilli(),
	); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := settleSampleSeed(ctx, env.db, lost, slog.Default()); err != nil {
		t.Fatalf("with the marker stored: err = %v, want nil", err)
	}
}
