// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type sampleSeedDeps struct {
	db         *store.DB
	evaluators *store.EvalEvaluatorRepo
}

func NewSampleSeedDeps(db *store.DB, evaluators *store.EvalEvaluatorRepo) sampleSeedDeps {
	return sampleSeedDeps{db: db, evaluators: evaluators}
}

const (
	sampleDatasetName    = "Sample Evaluation Dataset"
	sampleDatasetDesc    = "Preconfigured sample dataset bundled with SAM. Used by the 'Run Sample Experiment' button on the Reports page."
	sampleExperimentName = "Sample Experiment"
	sampleExperimentDesc = "Preconfigured sample so you can see a finished evaluation report in one click. Targets the built-in Orchestrator and uses your 'general' model alias."
	sampleTargetAgent    = "Orchestrator"
	sampleModelsJSON     = `[{"model":"general","alias":"general"}]`

	sampleEvaluatorName = "Closed QA"

	sampleSeedMarkerKey = "sample_experiment"
)

var (
	sampleExpectedFour  = "4"
	sampleExpectedParis = "Paris"
)

var sampleExamples = []store.EvalExample{
	{SequenceNumber: 1, Prompt: "What is 2+2?", ExpectedResponse: &sampleExpectedFour},
	{SequenceNumber: 2, Prompt: "What is the capital of France?", ExpectedResponse: &sampleExpectedParis},
	{SequenceNumber: 3, Prompt: "Summarize what an orchestrator agent does in one sentence."},
	{SequenceNumber: 4, Prompt: "List three benefits of using a multi-agent system."},
}

func SeedSampleExperiment(ctx context.Context, deps sampleSeedDeps, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "eval-seed")

	evaluator, err := deps.evaluators.GetByName(ctx, sampleEvaluatorName)
	if err != nil {
		if errors.Is(err, store.ErrEvalNotFound) {
			return fmt.Errorf("seed sample experiment: built-in evaluator %q is missing (expected to be seeded by SeedBuiltinEvaluators)", sampleEvaluatorName)
		}
		return fmt.Errorf("seed sample experiment: locate %q evaluator: %w", sampleEvaluatorName, err)
	}

	seedErr := deps.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var markerCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM system_seed_state WHERE seed_key = ?`,
			sampleSeedMarkerKey,
		).Scan(&markerCount); err != nil {
			return fmt.Errorf("probe seed marker: %w", err)
		}
		if markerCount > 0 {
			logger.Debug("sample experiment seed: marker present, skipping", "seedKey", sampleSeedMarkerKey)
			return nil
		}

		var existingExperimentID string
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM eval_experiments WHERE is_default = 1 ORDER BY created_time ASC, id ASC LIMIT 1`,
		).Scan(&existingExperimentID)
		if err == nil {
			logger.Debug("sample experiment seed: pre-marker rows detected, backfilling marker",
				"experimentID", existingExperimentID)
			return writeSampleSeedMarker(ctx, tx)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("probe existing experiment: %w", err)
		}

		var orphanDatasetID string
		err = tx.QueryRowContext(ctx,
			`SELECT id FROM eval_datasets WHERE is_default = 1 ORDER BY created_time ASC, id ASC LIMIT 1`,
		).Scan(&orphanDatasetID)
		if err == nil {
			logger.Warn("sample experiment seed: orphan default dataset present, marking as seeded",
				"datasetID", orphanDatasetID,
				"reason", "operator deleted the experiment, or pre-transaction-fix orphan; marker set so subsequent boots skip cleanly")
			return writeSampleSeedMarker(ctx, tx)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("probe orphan dataset: %w", err)
		}

		now := time.Now().UnixMilli()
		datasetID := idgen.New()
		experimentID := idgen.New()

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_datasets
			 (id, name, description, is_default, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, 'system', 'system', ?, ?)`,
			datasetID, sampleDatasetName, sampleDatasetDesc, dbutil.BoolToInt(true), now, now,
		); err != nil {
			return fmt.Errorf("insert dataset: %w", err)
		}

		for _, ex := range sampleExamples {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO eval_examples
				 (id, dataset_id, sequence_number, prompt, expected_response, created_time, updated_time)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				idgen.New(), datasetID, ex.SequenceNumber, ex.Prompt, ex.ExpectedResponse, now, now,
			); err != nil {
				return fmt.Errorf("insert example %d: %w", ex.SequenceNumber, err)
			}
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_experiments
			 (id, name, description, dataset_id, target_agent, models, runs_per_example, max_workers,
			  primary_evaluator_id, is_default, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, 1, 4, ?, ?, 'system', 'system', ?, ?)`,
			experimentID, sampleExperimentName, sampleExperimentDesc, datasetID,
			sampleTargetAgent, sampleModelsJSON, evaluator.ID, dbutil.BoolToInt(true), now, now,
		); err != nil {
			return fmt.Errorf("insert experiment: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_experiment_evaluators (experiment_id, evaluator_id) VALUES (?, ?)`,
			experimentID, evaluator.ID,
		); err != nil {
			return fmt.Errorf("attach evaluator: %w", err)
		}

		if err := writeSampleSeedMarker(ctx, tx); err != nil {
			return err
		}

		logger.Info("sample experiment seeded",
			"experimentID", experimentID, "datasetID", datasetID,
			"evaluator", evaluator.Name, "examples", len(sampleExamples))
		return nil
	})
	return settleSampleSeed(ctx, deps.db, seedErr, logger)
}

func settleSampleSeed(ctx context.Context, db *store.DB, seedErr error, logger *slog.Logger) error {
	if seedErr == nil {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM system_seed_state WHERE seed_key = ?`,
		sampleSeedMarkerKey,
	).Scan(&count); err != nil || count == 0 {
		return seedErr
	}
	logger.Debug("sample experiment seed: another instance seeded it", "error", seedErr)
	return nil
}

func writeSampleSeedMarker(ctx context.Context, tx *dbutil.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO system_seed_state (seed_key, seeded_at) VALUES (?, ?)
		 ON CONFLICT (seed_key) DO NOTHING`,
		sampleSeedMarkerKey, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("write seed marker: %w", err)
	}
	return nil
}
