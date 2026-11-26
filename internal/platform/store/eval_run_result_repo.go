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

type EvalRunResultWithScores struct {
	*EvalRunResult
	Scores []*EvalRunScore
}

type EvalRunResultRepo struct {
	db *DB
}

func NewEvalRunResultRepo(db *DB) *EvalRunResultRepo {
	return &EvalRunResultRepo{db: db}
}

func (r *EvalRunResultRepo) ListByRun(ctx context.Context, runID string) ([]*EvalRunResultWithScores, error) {
	results, err := dbutil.QueryRowPtrs[EvalRunResult](ctx, r.db,
		evalRunResultSelectWithExample+` WHERE r.run_id = ? ORDER BY e.sequence_number ASC, r.trial_index ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("EvalRunResultRepo.ListByRun: %w", err)
	}
	scores, err := dbutil.QueryRowPtrs[EvalRunScore](ctx, r.db, evalRunScoreByRunSelect+` WHERE rr.run_id = ?`, runID)
	if err != nil {
		return nil, fmt.Errorf("EvalRunResultRepo.ListByRun scores: %w", err)
	}
	return withScores(results, scores), nil
}

func (r *EvalRunResultRepo) ListByRunIDs(ctx context.Context, runIDs []string) ([]*EvalRunResultWithScores, error) {
	if len(runIDs) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(runIDs)
	if err != nil {
		return nil, fmt.Errorf("EvalRunResultRepo.ListByRunIDs: %w", err)
	}
	q := fmt.Sprintf(`%s WHERE r.run_id IN (%s) ORDER BY e.sequence_number ASC, r.trial_index ASC`, evalRunResultSelectWithExample, ph)
	results, err := dbutil.QueryRowPtrs[EvalRunResult](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf("EvalRunResultRepo.ListByRunIDs: %w", err)
	}
	scores, err := dbutil.QueryRowPtrs[EvalRunScore](ctx, r.db, fmt.Sprintf(`%s WHERE rr.run_id IN (%s)`, evalRunScoreByRunSelect, ph), args...)
	if err != nil {
		return nil, fmt.Errorf("EvalRunResultRepo.ListByRunIDs scores: %w", err)
	}
	return withScores(results, scores), nil
}

func (r *EvalRunResultRepo) GetByID(ctx context.Context, id string) (*EvalRunResultWithScores, error) {
	row, err := dbutil.QueryRow[EvalRunResult](ctx, r.db, evalRunResultSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalRunResultRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	enriched, err := r.attachScores(ctx, []*EvalRunResult{row})
	if err != nil {
		return nil, err
	}
	return enriched[0], nil
}

func (r *EvalRunResultRepo) Create(ctx context.Context, res *EvalRunResult) error {
	if res.ID == "" {
		res.ID = idgen.New()
	}
	if res.CreatedTime == 0 {
		res.CreatedTime = time.Now().UnixMilli()
	}
	_, err := r.db.ExecContext(ctx,
		evalRunResultInsert, evalRunResultInsertArgs(res)...)
	if err != nil {
		return fmt.Errorf("EvalRunResultRepo.Create: %w", err)
	}
	return nil
}

func (r *EvalRunResultRepo) CreateWithFiles(ctx context.Context, res *EvalRunResult, writeFiles func(ctx context.Context)) error {
	if res.ID == "" {
		res.ID = idgen.New()
	}
	if res.CreatedTime == 0 {
		res.CreatedTime = time.Now().UnixMilli()
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx, evalRunResultInsert, evalRunResultInsertArgs(res)...); err != nil {
			return fmt.Errorf("EvalRunResultRepo.CreateWithFiles: %w", err)
		}
		if writeFiles == nil {
			return nil
		}
		writeFiles(ctx)
		if _, err := tx.ExecContext(ctx,
			`UPDATE eval_run_results SET artifacts_metadata = ?, execution_data_key = ?, task_events_key = ?,
			   input_artifact_metadata = ?, expected_artifact_metadata = ? WHERE id = ?`,
			res.ArtifactsMetadata, res.ExecutionDataKey, res.TaskEventsKey,
			res.InputArtifactMetadata, res.ExpectedArtifactMetadata, res.ID); err != nil {
			return fmt.Errorf("EvalRunResultRepo.CreateWithFiles record files: %w", err)
		}
		return nil
	})
}

func (r *EvalRunResultRepo) BulkCreate(ctx context.Context, items []*EvalRunResult) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		for _, res := range items {
			if res.ID == "" {
				res.ID = idgen.New()
			}
			if res.CreatedTime == 0 {
				res.CreatedTime = now
			}
			_, err := tx.ExecContext(ctx,
				evalRunResultInsert, evalRunResultInsertArgs(res)...)
			if err != nil {
				return fmt.Errorf("insert run result: %w", err)
			}
		}
		return nil
	})
}

func (r *EvalRunResultRepo) SetExecutionDataKey(ctx context.Context, resultID, key string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE eval_run_results SET execution_data_key = ? WHERE id = ?`, key, resultID)
	if err != nil {
		return fmt.Errorf("EvalRunResultRepo.SetExecutionDataKey: %w", err)
	}
	return nil
}

func (r *EvalRunResultRepo) SetTaskEventsKey(ctx context.Context, resultID, key string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE eval_run_results SET task_events_key = ? WHERE id = ?`, key, resultID)
	if err != nil {
		return fmt.Errorf("EvalRunResultRepo.SetTaskEventsKey: %w", err)
	}
	return nil
}

func (r *EvalRunResultRepo) attachScores(ctx context.Context, results []*EvalRunResult) ([]*EvalRunResultWithScores, error) {
	out := make([]*EvalRunResultWithScores, len(results))
	if len(results) == 0 {
		return out, nil
	}
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("attach scores: %w", err)
	}
	q := fmt.Sprintf(`%s WHERE run_result_id IN (%s)`, evalRunScoreSelect, ph)
	scores, err := dbutil.QueryRowPtrs[EvalRunScore](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf("attach scores: %w", err)
	}
	return withScores(results, scores), nil
}

const evalRunScoreByRunSelect = `SELECT s.id, s.run_result_id, s.evaluator_id, s.score, s.choice, s.passed, s.reasoning, s.details, s.errored, s.created_time
	FROM eval_run_scores s JOIN eval_run_results rr ON rr.id = s.run_result_id`

func withScores(results []*EvalRunResult, scores []*EvalRunScore) []*EvalRunResultWithScores {
	scoresByResult := map[string][]*EvalRunScore{}
	for _, s := range scores {
		scoresByResult[s.RunResultID] = append(scoresByResult[s.RunResultID], s)
	}
	out := make([]*EvalRunResultWithScores, len(results))
	for i, r := range results {
		out[i] = &EvalRunResultWithScores{EvalRunResult: r, Scores: scoresByResult[r.ID]}
	}
	return out
}

const evalRunResultColumns = `id, run_id, example_id, trial_index, input_prompt, expected_response,
	target_agent, target_type, agent_response, resolved_model_name, execution_data_key, task_events_key,
	artifacts_metadata, input_artifact_metadata, expected_artifact_metadata, duration_seconds, error_message, failure_kind, created_time,
	input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, total_tokens, token_breakdown`

const evalRunResultInsert = `INSERT INTO eval_run_results (` + evalRunResultColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func evalRunResultInsertArgs(res *EvalRunResult) []any {
	return []any{
		res.ID, res.RunID, res.ExampleID, res.TrialIndex, res.InputPrompt, res.ExpectedResponse,
		res.TargetAgent, res.TargetType, res.AgentResponse, res.ResolvedModelName, res.ExecutionDataKey, res.TaskEventsKey,
		res.ArtifactsMetadata, res.InputArtifactMetadata, res.ExpectedArtifactMetadata, res.DurationSeconds, res.ErrorMessage, res.FailureKind, res.CreatedTime,
		res.InputTokens, res.OutputTokens, res.CachedInputTokens, res.ReasoningTokens, res.TotalTokens, res.TokenBreakdown,
	}
}

const evalRunResultSelect = `SELECT ` + evalRunResultColumns + `
	FROM eval_run_results`

const evalRunResultSelectWithExample = `SELECT r.id, r.run_id, r.example_id, r.trial_index, r.input_prompt, r.expected_response,
	r.target_agent, r.target_type, r.agent_response, r.resolved_model_name, r.execution_data_key, r.task_events_key,
	r.artifacts_metadata, r.input_artifact_metadata, r.expected_artifact_metadata, r.duration_seconds, r.error_message, r.failure_kind, r.created_time,
	r.input_tokens, r.output_tokens, r.cached_input_tokens, r.reasoning_tokens, r.total_tokens, r.token_breakdown
	FROM eval_run_results r
	JOIN eval_examples e ON r.example_id = e.id`

func (r *EvalRunResultRepo) ListByRunPaginated(ctx context.Context, runID string, pageNumber, pageSize int) ([]*EvalRunResultWithScores, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_run_results r JOIN eval_examples e ON r.example_id = e.id WHERE r.run_id = ?`, runID)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunResultRepo.ListByRunPaginated count: %w", err)
	}
	results, err := dbutil.QueryRowPtrs[EvalRunResult](ctx, r.db,
		evalRunResultSelectWithExample+` WHERE r.run_id = ? ORDER BY e.sequence_number ASC, r.trial_index ASC, r.id ASC LIMIT ? OFFSET ?`,
		runID, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunResultRepo.ListByRunPaginated: %w", err)
	}
	enriched, err := r.attachScores(ctx, results)
	if err != nil {
		return nil, 0, err
	}
	return enriched, total, nil
}
