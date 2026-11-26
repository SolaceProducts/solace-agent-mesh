// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func (r *EvalRunRepo) ListPendingIDsBefore(ctx context.Context, cutoff int64) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM eval_runs WHERE status = 'pending' AND started_time < ?`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.ListPendingIDsBefore: %w", err)
	}
	return ids, nil
}

func (r *EvalRunRepo) TakeOver(ctx context.Context, id string) (int, bool, error) {
	counts, err := dbutil.QueryVals[int](ctx, r.db,
		`UPDATE eval_runs SET takeovers = takeovers + 1, coordinator_started_at = ?
		 WHERE id = ? AND status = 'running' RETURNING takeovers`, time.Now().UnixMilli(), id)
	if err != nil {
		return 0, false, fmt.Errorf("EvalRunRepo.TakeOver(%s): %w", id, err)
	}
	if len(counts) == 0 {
		return 0, false, nil
	}
	return counts[0], true, nil
}

func (r *EvalRunRepo) DeleteHalfSavedResults(ctx context.Context, runID string, evaluatorIDs []string) (int, error) {
	if len(evaluatorIDs) == 0 {
		return 0, nil
	}
	ph, args, err := dbutil.InClause(evaluatorIDs)
	if err != nil {
		return 0, fmt.Errorf("EvalRunRepo.DeleteHalfSavedResults: %w", err)
	}
	all := append([]any{runID}, args...)
	all = append(all, len(evaluatorIDs), runID)
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM eval_run_results
		 WHERE run_id = ? AND error_message IS NULL
		   AND (SELECT COUNT(DISTINCT s.evaluator_id) FROM eval_run_scores s
		        WHERE s.run_result_id = eval_run_results.id AND s.evaluator_id IN (`+ph+`)) < ?
		   AND EXISTS (SELECT 1 FROM eval_runs WHERE id = ? AND status = 'running')`, all...)
	if err != nil {
		return 0, fmt.Errorf("EvalRunRepo.DeleteHalfSavedResults: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *EvalRunRepo) ResetProgress(ctx context.Context, id string, n int) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE eval_runs SET progress_current = ? WHERE id = ? AND status = 'running'`, n, id); err != nil {
		return fmt.Errorf("EvalRunRepo.ResetProgress: %w", err)
	}
	return nil
}

func (r *EvalRunRepo) FailRunning(ctx context.Context, id, message string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = 'failed', error_message = ?, completed_time = ?
		 WHERE id = ? AND status = 'running'`, message, time.Now().UnixMilli(), id)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.FailRunning: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *EvalRunRepo) CancelActive(ctx context.Context, id string) (*EvalRun, bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = 'cancelled', completed_time = ?
		 WHERE id = ? AND status IN ('pending', 'running')`, time.Now().UnixMilli(), id)
	if err != nil {
		return nil, false, fmt.Errorf("EvalRunRepo.CancelActive: %w", err)
	}
	n, _ := res.RowsAffected()
	row, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return row, n > 0, nil
}
