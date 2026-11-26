// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type EvalFileDeletes struct {
	RunIDs         []string
	TrialSnapshots []EvalTrialSnapshot
	ExampleFiles   []EvalExampleFile
}

func (d EvalFileDeletes) Empty() bool {
	return len(d.RunIDs) == 0 && len(d.TrialSnapshots) == 0 && len(d.ExampleFiles) == 0
}

type EvalTrialSnapshot struct {
	RunID             string `db:"run_id"`
	ExampleID         string `db:"example_id"`
	TrialIndex        int    `db:"trial_index"`
	ArtifactsMetadata string `db:"artifacts_metadata"`
}

type EvalExampleFile struct {
	SessionID string `db:"artifact_session_id"`
	Filename  string `db:"filename"`
}

type EvalFileDeleteEvent func(EvalFileDeletes) (*OutboxEvent, error)

type evalFileScope struct {
	runWhere     string
	runArgs      []any
	exampleWhere string
	exampleArgs  []any
}

func recordFileDeletes(ctx context.Context, tx *dbutil.Tx, scope evalFileScope, build EvalFileDeleteEvent) error {
	if build == nil {
		return nil
	}
	var deletes EvalFileDeletes
	var err error
	if scope.runWhere != "" {
		if deletes.RunIDs, err = dbutil.QueryVals[string](ctx, tx,
			`SELECT id FROM eval_runs WHERE `+scope.runWhere, scope.runArgs...); err != nil {
			return fmt.Errorf("list deleted runs: %w", err)
		}
	}
	var trialWhere []string
	var trialArgs []any
	if scope.runWhere != "" {
		trialWhere = append(trialWhere, `run_id IN (SELECT id FROM eval_runs WHERE `+scope.runWhere+`)`)
		trialArgs = append(trialArgs, scope.runArgs...)
	}
	if scope.exampleWhere != "" {
		trialWhere = append(trialWhere, `example_id IN (SELECT id FROM eval_examples WHERE `+scope.exampleWhere+`)`)
		trialArgs = append(trialArgs, scope.exampleArgs...)
		if deletes.ExampleFiles, err = dbutil.QueryRows[EvalExampleFile](ctx, tx,
			`SELECT artifact_session_id, filename FROM eval_example_artifacts
			 WHERE example_id IN (SELECT id FROM eval_examples WHERE `+scope.exampleWhere+`)`, scope.exampleArgs...); err != nil {
			return fmt.Errorf("list deleted example files: %w", err)
		}
	}
	if len(trialWhere) > 0 {
		where := trialWhere[0]
		if len(trialWhere) == 2 {
			where = `(` + trialWhere[0] + ` OR ` + trialWhere[1] + `)`
		}
		if deletes.TrialSnapshots, err = dbutil.QueryRows[EvalTrialSnapshot](ctx, tx,
			`SELECT run_id, example_id, trial_index, artifacts_metadata FROM eval_run_results
			 WHERE artifacts_metadata IS NOT NULL AND `+where, trialArgs...); err != nil {
			return fmt.Errorf("list deleted trial snapshots: %w", err)
		}
	}
	return writeFileDeleteEvent(ctx, tx, deletes, build)
}

func writeFileDeleteEvent(ctx context.Context, tx *dbutil.Tx, deletes EvalFileDeletes, build EvalFileDeleteEvent) error {
	if build == nil || deletes.Empty() {
		return nil
	}
	event, err := build(deletes)
	if err != nil {
		return fmt.Errorf("build file delete event: %w", err)
	}
	if event == nil {
		return nil
	}
	if err := (&OutboxRepo{}).CreateInTx(ctx, tx, event); err != nil {
		return fmt.Errorf("write file delete event: %w", err)
	}
	return nil
}

func refuseActiveRuns(ctx context.Context, tx *dbutil.Tx, where string, args ...any) error {
	n, err := dbutil.QueryVal[int](ctx, tx,
		`SELECT COUNT(*) FROM eval_runs WHERE status IN ('pending', 'running') AND `+where, args...)
	if err != nil {
		return fmt.Errorf("check active runs: %w", err)
	}
	if n > 0 {
		return ErrEvalRunActive
	}
	return nil
}
