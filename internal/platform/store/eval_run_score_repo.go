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
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type EvalRunScoreRepo struct {
	db *DB
}

func NewEvalRunScoreRepo(db *DB) *EvalRunScoreRepo {
	return &EvalRunScoreRepo{db: db}
}

func (r *EvalRunScoreRepo) ListByResult(ctx context.Context, runResultID string) ([]*EvalRunScore, error) {
	return dbutil.QueryRowPtrs[EvalRunScore](ctx, r.db,
		evalRunScoreSelect+` WHERE run_result_id = ?`, runResultID)
}

func (r *EvalRunScoreRepo) ListByRun(ctx context.Context, runID string) ([]*EvalRunScore, error) {
	return dbutil.QueryRowPtrs[EvalRunScore](ctx, r.db,
		`SELECT s.id, s.run_result_id, s.evaluator_id, s.score, s.choice, s.passed, s.reasoning, s.details, s.errored, s.created_time
		 FROM eval_run_scores s
		 JOIN eval_run_results rr ON rr.id = s.run_result_id
		 WHERE rr.run_id = ?`, runID)
}

func passedArg(p *bool) any {
	if p == nil {
		return nil
	}
	return dbutil.BoolToInt(*p)
}

func (r *EvalRunScoreRepo) Create(ctx context.Context, s *EvalRunScore) error {
	if s.ID == "" {
		s.ID = idgen.New()
	}
	if s.CreatedTime == 0 {
		s.CreatedTime = time.Now().UnixMilli()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO eval_run_scores
		 (id, run_result_id, evaluator_id, score, choice, passed, reasoning, details, errored, created_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.RunResultID, s.EvaluatorID, s.Score, s.Choice, passedArg(s.Passed), s.Reasoning, s.Details, passedArg(s.Errored), s.CreatedTime,
	)
	if err != nil {
		return fmt.Errorf("EvalRunScoreRepo.Create: %w", err)
	}
	return nil
}

func (r *EvalRunScoreRepo) BulkCreate(ctx context.Context, items []*EvalRunScore) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		for _, s := range items {
			if s.ID == "" {
				s.ID = idgen.New()
			}
			if s.CreatedTime == 0 {
				s.CreatedTime = now
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO eval_run_scores
				 (id, run_result_id, evaluator_id, score, choice, passed, reasoning, details, errored, created_time)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				s.ID, s.RunResultID, s.EvaluatorID, s.Score, s.Choice, passedArg(s.Passed), s.Reasoning, s.Details, passedArg(s.Errored), s.CreatedTime,
			)
			if err != nil {
				return fmt.Errorf("insert run score: %w", err)
			}
		}
		return nil
	})
}

func (r *EvalRunScoreRepo) AverageScoresByEvaluator(ctx context.Context, runID string) (map[string]float64, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.evaluator_id, AVG(s.score)
		 FROM eval_run_scores s
		 JOIN eval_run_results rr ON rr.id = s.run_result_id
		 WHERE rr.run_id = ?
		 GROUP BY s.evaluator_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("EvalRunScoreRepo.AverageScoresByEvaluator: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]float64{}
	for rows.Next() {
		var evaluatorID string
		var avg float64
		if err := rows.Scan(&evaluatorID, &avg); err != nil {
			return nil, fmt.Errorf("EvalRunScoreRepo.AverageScoresByEvaluator scan: %w", err)
		}
		out[evaluatorID] = avg
	}
	return out, rows.Err()
}

const evalRunScoreSelect = `SELECT id, run_result_id, evaluator_id, score, choice, passed, reasoning, details, errored, created_time
	FROM eval_run_scores`
