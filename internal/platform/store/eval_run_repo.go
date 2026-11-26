// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type EvalRunGroupSummary struct {
	RunGroupID     string
	ExperimentID   string
	ExperimentName string
	TargetAgent    string
	TargetType     string
	RunName        *string
	StartedTime    int64
	CompletedTime  *int64
	RunCount       int
	Statuses       []string
}

type EvalRunRepo struct {
	db *DB

	beforeSummaryScoresWrite func(id string)
}

func NewEvalRunRepo(db *DB) *EvalRunRepo {
	return &EvalRunRepo{db: db}
}

var activeStatuses = []string{"pending", "running"}

func (r *EvalRunRepo) HasActiveRuns(ctx context.Context, experimentID string) (bool, error) {
	active, err := hasActiveRuns(ctx, r.db, experimentID)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.HasActiveRuns: %w", err)
	}
	return active, nil
}

func hasActiveRuns(ctx context.Context, q dbutil.Querier, experimentID string) (bool, error) {
	ph, args, err := dbutil.InClause(activeStatuses)
	if err != nil {
		return false, err
	}
	allArgs := append([]any{experimentID}, args...)
	n, err := dbutil.QueryVal[int](ctx, q,
		fmt.Sprintf(`SELECT COUNT(*) FROM eval_runs WHERE experiment_id = ? AND status IN (%s)`, ph), allArgs...)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *EvalRunRepo) HasActiveRunsForDataset(ctx context.Context, datasetID string) (bool, error) {
	ph, args, err := dbutil.InClause(activeStatuses)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.HasActiveRunsForDataset: %w", err)
	}
	allArgs := append([]any{datasetID}, args...)
	n, err := dbutil.QueryVal[int](ctx, r.db,
		fmt.Sprintf(`SELECT COUNT(*) FROM eval_runs r
		 JOIN eval_experiments e ON e.id = r.experiment_id
		 WHERE e.dataset_id = ? AND r.status IN (%s)`, ph), allArgs...)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.HasActiveRunsForDataset: %w", err)
	}
	return n > 0, nil
}

func (r *EvalRunRepo) ListByExperiment(ctx context.Context, experimentID string) ([]*EvalRun, error) {
	return dbutil.QueryRowPtrs[EvalRun](ctx, r.db,
		evalRunSelect+` WHERE experiment_id = ? ORDER BY started_time DESC`, experimentID)
}

func (r *EvalRunRepo) ListByRunGroup(ctx context.Context, runGroupID string) ([]*EvalRun, error) {
	return dbutil.QueryRowPtrs[EvalRun](ctx, r.db,
		evalRunSelect+` WHERE run_group_id = ? ORDER BY model ASC, started_time ASC`, runGroupID)
}

func (r *EvalRunRepo) GetByID(ctx context.Context, id string) (*EvalRun, error) {
	row, err := dbutil.QueryRow[EvalRun](ctx, r.db, evalRunSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalRunRepo) Create(ctx context.Context, run *EvalRun) error {
	if err := insertRun(ctx, r.db, run); err != nil {
		return fmt.Errorf("EvalRunRepo.Create: %w", err)
	}
	return nil
}

func (r *EvalRunRepo) CreateIfIdle(ctx context.Context, experimentID string, runs []*EvalRun) error {
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		lock := `SELECT id FROM eval_experiments WHERE id = ?`
		if tx.Dialect() == "postgres" {
			lock += ` FOR UPDATE`
		}
		ids, err := dbutil.QueryVals[string](ctx, tx, lock, experimentID)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return ErrEvalNotFound
		}
		active, err := hasActiveRuns(ctx, tx, experimentID)
		if err != nil {
			return err
		}
		if active {
			return ErrEvalRunActive
		}
		for _, run := range runs {
			if err := insertRun(ctx, tx, run); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("EvalRunRepo.CreateIfIdle: %w", err)
	}
	return nil
}

func insertRun(ctx context.Context, q dbutil.Querier, run *EvalRun) error {
	if run.ID == "" {
		run.ID = idgen.New()
	}
	if run.RunGroupID == "" {
		run.RunGroupID = idgen.New()
	}
	if run.Status == "" {
		run.Status = "pending"
	}
	if run.Source == "" {
		run.Source = "manual"
	}
	if run.StartedTime == 0 {
		run.StartedTime = time.Now().UnixMilli()
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO eval_runs
		 (id, experiment_id, run_group_id, status, progress_current, progress_total, error_message,
		  summary_scores, model, config_snapshot, evaluator_snapshot, run_name, source, run_as,
		  started_by, started_time, completed_time, coordinator_started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ExperimentID, run.RunGroupID, run.Status, run.ProgressCurrent, run.ProgressTotal, run.ErrorMessage,
		run.SummaryScores, run.Model, run.ConfigSnapshot, run.EvaluatorSnapshot, run.RunName, run.Source, run.RunAs,
		run.StartedBy, run.StartedTime, run.CompletedTime, run.CoordinatorStartedAt,
	)
	return err
}

func (r *EvalRunRepo) Delete(ctx context.Context, id string, files EvalFileDeleteEvent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := refuseActiveRuns(ctx, tx, `id = ?`, id); err != nil {
			return err
		}
		if err := recordFileDeletes(ctx, tx, evalFileScope{runWhere: `id = ?`, runArgs: []any{id}}, files); err != nil {
			return fmt.Errorf("EvalRunRepo.Delete(%s): %w", id, err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM eval_runs WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("EvalRunRepo.Delete(%s): %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrEvalNotFound
		}
		return nil
	})
}

func (r *EvalRunRepo) DeleteByRunGroup(ctx context.Context, runGroupID string, files EvalFileDeleteEvent) (int, error) {
	var deleted int64
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := refuseActiveRuns(ctx, tx, `run_group_id = ?`, runGroupID); err != nil {
			return err
		}
		if err := recordFileDeletes(ctx, tx, evalFileScope{runWhere: `run_group_id = ?`, runArgs: []any{runGroupID}}, files); err != nil {
			return fmt.Errorf("EvalRunRepo.DeleteByRunGroup: %w", err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM eval_runs WHERE run_group_id = ?`, runGroupID)
		if err != nil {
			return fmt.Errorf("EvalRunRepo.DeleteByRunGroup: %w", err)
		}
		deleted, _ = res.RowsAffected()
		return nil
	})
	return int(deleted), err
}

func (r *EvalRunRepo) UpdateStatus(ctx context.Context, id, status string) (*EvalRun, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	existing.Status = status
	if status == "completed" || status == "failed" || status == "cancelled" {
		now := time.Now().UnixMilli()
		existing.CompletedTime = &now
	}
	_, err = r.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = ?, completed_time = ? WHERE id = ?`,
		existing.Status, existing.CompletedTime, id)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.UpdateStatus: %w", err)
	}
	return existing, nil
}

func (r *EvalRunRepo) UpdateProgress(ctx context.Context, id string, current int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET progress_current = ?, takeovers = 0 WHERE id = ? AND progress_current < ?`, current, id, current)
	if err != nil {
		return fmt.Errorf("EvalRunRepo.UpdateProgress: %w", err)
	}
	return nil
}

func (r *EvalRunRepo) SetEvaluatorStats(ctx context.Context, id, stats string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET evaluator_stats = ? WHERE id = ?`, stats, id); err != nil {
		return fmt.Errorf("EvalRunRepo.SetEvaluatorStats: %w", err)
	}
	return nil
}

func (r *EvalRunRepo) UpdateSummaryScores(
	ctx context.Context,
	id string,
	summaryScores string,
	status string,
	errorMessage *string,
) (*EvalRun, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Status == "cancelled" {
		return existing, nil
	}
	if r.beforeSummaryScoresWrite != nil {
		r.beforeSummaryScoresWrite(id)
	}
	now := time.Now().UnixMilli()
	res, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET summary_scores = ?, status = ?, completed_time = ?, error_message = ?
		 WHERE id = ? AND status != 'cancelled'`,
		summaryScores, status, now, errorMessage, id)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.UpdateSummaryScores: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.UpdateSummaryScores: rows affected: %w", err)
	}
	if affected == 0 {
		return r.GetByID(ctx, id)
	}
	existing.SummaryScores = &summaryScores
	existing.Status = status
	existing.CompletedTime = &now
	if errorMessage != nil {
		existing.ErrorMessage = errorMessage
	}
	return existing, nil
}

func (r *EvalRunRepo) SetRunning(ctx context.Context, id string, progressTotal int) (bool, error) {
	now := time.Now().UnixMilli()
	res, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = 'running', progress_total = ?, coordinator_started_at = ?
		 WHERE id = ? AND status = 'pending'`, progressTotal, now, id)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.SetRunning: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *EvalRunRepo) ListStaleRunIDs(ctx context.Context, budgetSeconds float64) ([]string, error) {
	type cand struct {
		ID                   string `db:"id"`
		ProgressTotal        int    `db:"progress_total"`
		CoordinatorStartedAt *int64 `db:"coordinator_started_at"`
	}
	candidates, err := dbutil.QueryRowPtrs[cand](ctx, r.db,
		`SELECT id, progress_total, coordinator_started_at FROM eval_runs WHERE status = 'running'`)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.ListStaleRunIDs: %w", err)
	}
	now := time.Now().UnixMilli()
	stale := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c.CoordinatorStartedAt == nil || c.ProgressTotal <= 0 {
			continue
		}
		deadlineMs := *c.CoordinatorStartedAt + int64(float64(c.ProgressTotal)*budgetSeconds*1000)
		if deadlineMs < now {
			stale = append(stale, c.ID)
		}
	}
	return stale, nil
}

func (r *EvalRunRepo) DeriveFinalStatusFromResults(ctx context.Context, runID string) (string, error) {
	type counts struct {
		Total   int `db:"total"`
		Errored int `db:"errored"`
		Flagged int `db:"flagged"`
	}
	rows, err := dbutil.QueryRowPtrs[counts](ctx, r.db,
		`SELECT COUNT(*) AS total,
		        COUNT(CASE WHEN error_message IS NOT NULL THEN 1 END) AS errored,
		        COUNT(CASE WHEN error_message IS NULL AND failure_kind = ? THEN 1 END) AS flagged
		 FROM eval_run_results WHERE run_id = ?`, EvalFailureKindPartialFailure, runID)
	if err != nil {
		return "", fmt.Errorf("EvalRunRepo.DeriveFinalStatusFromResults: %w", err)
	}
	if len(rows) == 0 || rows[0].Total == 0 {
		return "failed", nil
	}
	successful := rows[0].Total - rows[0].Errored
	switch {
	case successful == 0:
		return "failed", nil
	case rows[0].Errored == 0 && rows[0].Flagged == 0:
		return "completed", nil
	default:
		return "completed_with_warnings", nil
	}
}

func (r *EvalRunRepo) FinalizeStaleRun(ctx context.Context, runID, errorMessage string) (bool, error) {
	status, err := r.DeriveFinalStatusFromResults(ctx, runID)
	if err != nil {
		return false, err
	}
	now := time.Now().UnixMilli()
	var errMsgArg any
	if errorMessage != "" {
		errMsgArg = errorMessage
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE eval_runs SET status = ?, error_message = ?, completed_time = ?
		 WHERE id = ? AND status = 'running'`,
		status, errMsgArg, now, runID)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.FinalizeStaleRun: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.FinalizeStaleRun: rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *EvalRunRepo) ListIDsByStatus(ctx context.Context, status string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM eval_runs WHERE status = ?`, status)
}

func (r *EvalRunRepo) ListDistinctTargetAgents(ctx context.Context) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT DISTINCT e.target_agent FROM eval_experiments e
		 WHERE e.target_agent IS NOT NULL AND e.target_agent != ''
		   AND EXISTS (SELECT 1 FROM eval_runs r WHERE r.experiment_id = e.id)
		 ORDER BY e.target_agent ASC`)
}

type RunGroupFilter struct {
	Name       string
	Agent      string
	TargetType string
}

func (r *EvalRunRepo) ListRunGroupsPaginated(
	ctx context.Context,
	pageNumber, pageSize int,
	filter RunGroupFilter,
) ([]*EvalRunGroupSummary, int, error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	args := []any{}
	clauses := []string{}
	if filter.Name != "" {
		pred, arg := dbutil.LikeContainsFold("g.run_name", filter.Name)
		clauses = append(clauses, pred)
		args = append(args, arg)
	}
	if filter.Agent != "" {
		clauses = append(clauses, "e.target_agent = ?")
		args = append(args, filter.Agent)
	}
	if filter.TargetType != "" {
		clauses = append(clauses, "e.target_type = ?")
		args = append(args, filter.TargetType)
	}
	whereClause := ""
	if len(clauses) > 0 {
		whereClause = "WHERE " + strings.Join(clauses, " AND ")
	}

	const statusSep = "\x1f"
	statusAgg := "GROUP_CONCAT(status, '" + statusSep + "')"
	if r.db.Dialect() == "postgres" {
		statusAgg = "string_agg(status, '" + statusSep + "')"
	}

	q := fmt.Sprintf(
		`WITH g AS (
		     SELECT run_group_id,
		            experiment_id,
		            MIN(started_time)   AS started_time,
		            MAX(completed_time) AS completed_time,
		            MAX(run_name)       AS run_name,
		            COUNT(*)            AS run_count,
		            %s                  AS statuses
		     FROM eval_runs
		     GROUP BY run_group_id, experiment_id
		 )
		 SELECT g.run_group_id, g.experiment_id, g.started_time, g.completed_time,
		        g.run_name, g.run_count, g.statuses,
		        e.name, e.target_agent, e.target_type,
		        COUNT(*) OVER() AS total_count
		 FROM g JOIN eval_experiments e ON e.id = g.experiment_id
		 %s
		 ORDER BY g.started_time DESC, g.run_group_id ASC
		 LIMIT ? OFFSET ?`, statusAgg, whereClause)

	pageArgs := append([]any{}, args...)
	pageArgs = append(pageArgs, pageSize, (pageNumber-1)*pageSize)

	rows, err := r.db.QueryContext(ctx, q, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListRunGroupsPaginated query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []*EvalRunGroupSummary
	total := 0
	for rows.Next() {
		s := &EvalRunGroupSummary{}
		var statusesRaw *string
		if scanErr := rows.Scan(&s.RunGroupID, &s.ExperimentID, &s.StartedTime, &s.CompletedTime,
			&s.RunName, &s.RunCount, &statusesRaw, &s.ExperimentName, &s.TargetAgent, &s.TargetType, &total); scanErr != nil {
			return nil, 0, fmt.Errorf("EvalRunRepo.ListRunGroupsPaginated scan: %w", scanErr)
		}
		if statusesRaw != nil && *statusesRaw != "" {
			s.Statuses = strings.Split(*statusesRaw, statusSep)
		}
		summaries = append(summaries, s)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListRunGroupsPaginated rows: %w", rowsErr)
	}

	if len(summaries) == 0 {
		countQ := fmt.Sprintf(
			`WITH g AS (
			     SELECT run_group_id, experiment_id, MAX(run_name) AS run_name
			     FROM eval_runs GROUP BY run_group_id, experiment_id
			 )
			 SELECT COUNT(*) FROM g JOIN eval_experiments e ON e.id = g.experiment_id %s`,
			whereClause)
		total, err = dbutil.QueryVal[int](ctx, r.db, countQ, args...)
		if err != nil {
			return nil, 0, fmt.Errorf("EvalRunRepo.ListRunGroupsPaginated count: %w", err)
		}
	}
	return summaries, total, nil
}

var completedRunStatuses = []string{"completed", "completed_with_warnings"}

type AgentExperimentRank struct {
	ExperimentID       string  `db:"id"`
	ExperimentName     string  `db:"name"`
	PrimaryEvaluatorID *string `db:"primary_evaluator_id"`
	Models             string  `db:"models"`
	RunGroupCount      int     `db:"run_group_count"`
}

func (r *EvalRunRepo) TopExperimentsForAgent(
	ctx context.Context,
	agentName string,
	acceptableModels []string,
	limit int,
) ([]*AgentExperimentRank, error) {
	if len(acceptableModels) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	const errWrap = "EvalRunRepo.TopExperimentsForAgent: %w"
	modelPH, modelArgs, err := dbutil.InClause(acceptableModels)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	statusPH, statusArgs, err := dbutil.InClause(completedRunStatuses)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	q := fmt.Sprintf(`
		SELECT e.id, e.name, e.primary_evaluator_id, e.models,
		       COUNT(DISTINCT r.run_group_id) AS run_group_count
		FROM eval_experiments e
		JOIN eval_runs r ON r.experiment_id = e.id
		WHERE e.target_agent = ?
		  AND e.target_type = ?
		  AND r.model IN (%s)
		  AND r.status IN (%s)
		  AND e.primary_evaluator_id IS NOT NULL
		GROUP BY e.id, e.name, e.primary_evaluator_id, e.models
		ORDER BY COUNT(DISTINCT r.run_group_id) DESC, e.id ASC
		LIMIT ?`, modelPH, statusPH)
	args := []any{agentName, EvalTargetTypeAgent}
	args = append(args, modelArgs...)
	args = append(args, statusArgs...)
	args = append(args, limit)
	rows, err := dbutil.QueryRowPtrs[AgentExperimentRank](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	return rows, nil
}

func (r *EvalRunRepo) TopExperimentsForWorkflow(ctx context.Context, workflowName string, limit int) ([]*AgentExperimentRank, error) {
	if limit <= 0 {
		limit = 3
	}
	const errWrap = "EvalRunRepo.TopExperimentsForWorkflow: %w"
	statusPH, statusArgs, err := dbutil.InClause(completedRunStatuses)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	q := fmt.Sprintf(`
		SELECT e.id, e.name, e.primary_evaluator_id, e.models,
		       COUNT(DISTINCT r.run_group_id) AS run_group_count
		FROM eval_experiments e
		JOIN eval_runs r ON r.experiment_id = e.id
		WHERE e.target_agent = ?
		  AND e.target_type = ?
		  AND r.status IN (%s)
		  AND e.primary_evaluator_id IS NOT NULL
		GROUP BY e.id, e.name, e.primary_evaluator_id, e.models
		ORDER BY COUNT(DISTINCT r.run_group_id) DESC, e.id ASC
		LIMIT ?`, statusPH)
	args := []any{workflowName, EvalTargetTypeWorkflow}
	args = append(args, statusArgs...)
	args = append(args, limit)
	rows, err := dbutil.QueryRowPtrs[AgentExperimentRank](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	return rows, nil
}

func (r *EvalRunRepo) HasWorkflowExperiment(ctx context.Context, workflowName string) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_experiments WHERE target_agent = ? AND target_type = ?`, workflowName, EvalTargetTypeWorkflow)
	if err != nil {
		return false, fmt.Errorf("EvalRunRepo.HasWorkflowExperiment: %w", err)
	}
	return n > 0, nil
}

func (r *EvalRunRepo) LastRunGroupsForWorkflowExperiment(ctx context.Context, experimentID string, limit int) ([]*EvalRun, error) {
	return r.lastRunGroups(ctx, experimentID, "", nil, limit, "EvalRunRepo.LastRunGroupsForWorkflowExperiment: %w")
}

func (r *EvalRunRepo) LastRunGroupsForExperiment(
	ctx context.Context,
	experimentID string,
	acceptableModels []string,
	limit int,
) ([]*EvalRun, error) {
	if len(acceptableModels) == 0 {
		return nil, nil
	}
	const errWrap = "EvalRunRepo.LastRunGroupsForExperiment: %w"
	modelPH, modelArgs, err := dbutil.InClause(acceptableModels)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	return r.lastRunGroups(ctx, experimentID, "AND model IN ("+modelPH+")", modelArgs, limit, errWrap)
}

func (r *EvalRunRepo) lastRunGroups(ctx context.Context, experimentID, modelClause string, modelArgs []any, limit int, errWrap string) ([]*EvalRun, error) {
	if limit <= 0 {
		limit = 10
	}
	statusPH, statusArgs, err := dbutil.InClause(completedRunStatuses)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	q := fmt.Sprintf(`
		WITH ranked AS (
		    SELECT id, experiment_id, run_group_id, status, progress_current, progress_total,
		           error_message, summary_scores, evaluator_stats, model, config_snapshot, evaluator_snapshot,
		           run_name, source, run_as, started_by, started_time, completed_time, coordinator_started_at,
		           ROW_NUMBER() OVER (PARTITION BY run_group_id ORDER BY started_time ASC, id ASC) AS rn
		    FROM eval_runs
		    WHERE experiment_id = ? %[1]s AND status IN (%[2]s)
		)
		SELECT id, experiment_id, run_group_id, status, progress_current, progress_total,
		       error_message, summary_scores, evaluator_stats, model, config_snapshot, evaluator_snapshot,
		       run_name, source, run_as, started_by, started_time, completed_time, coordinator_started_at
		FROM ranked
		WHERE rn = 1
		ORDER BY started_time DESC
		LIMIT ?`, modelClause, statusPH)
	args := []any{experimentID}
	args = append(args, modelArgs...)
	args = append(args, statusArgs...)
	args = append(args, limit)
	rows, err := dbutil.QueryRowPtrs[EvalRun](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf(errWrap, err)
	}
	slices.Reverse(rows)
	return rows, nil
}

func (r *EvalRunRepo) ListByExperimentPaginated(ctx context.Context, experimentID string, pageNumber, pageSize int) ([]*EvalRun, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM eval_runs WHERE experiment_id = ?`, experimentID)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListByExperimentPaginated count: %w", err)
	}
	rows, err := dbutil.QueryRowPtrs[EvalRun](ctx, r.db,
		evalRunSelect+` WHERE experiment_id = ? ORDER BY started_time DESC, id ASC LIMIT ? OFFSET ?`,
		experimentID, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListByExperimentPaginated: %w", err)
	}
	return rows, total, nil
}

func (r *EvalRunRepo) ListRunGroupIDsByExperiment(ctx context.Context, experimentID string, pageNumber, pageSize int) ([]string, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(DISTINCT run_group_id) FROM eval_runs WHERE experiment_id = ?`, experimentID)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListRunGroupIDsByExperiment count: %w", err)
	}
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT run_group_id FROM eval_runs WHERE experiment_id = ?
		 GROUP BY run_group_id ORDER BY MIN(started_time) DESC, run_group_id ASC LIMIT ? OFFSET ?`,
		experimentID, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListRunGroupIDsByExperiment: %w", err)
	}
	return ids, total, nil
}

func (r *EvalRunRepo) ListRunStatesByRunGroups(ctx context.Context, experimentID string, runGroupIDs []string) ([]*EvalRun, error) {
	ph, args, err := dbutil.InClause(runGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.ListRunStatesByRunGroups: %w", err)
	}
	if ph == "" {
		return nil, nil
	}
	rows, err := dbutil.QueryRowPtrs[EvalRun](ctx, r.db,
		fmt.Sprintf(`SELECT id, experiment_id, run_group_id, status, progress_current, progress_total,
			started_by, started_time, completed_time
			FROM eval_runs WHERE experiment_id = ? AND run_group_id IN (%s)
			ORDER BY model ASC, started_time ASC, id ASC`, ph), append([]any{experimentID}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("EvalRunRepo.ListRunStatesByRunGroups: %w", err)
	}
	return rows, nil
}

func (r *EvalRunRepo) ListByRunGroupPaginated(ctx context.Context, runGroupID string, pageNumber, pageSize int) ([]*EvalRun, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM eval_runs WHERE run_group_id = ?`, runGroupID)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListByRunGroupPaginated count: %w", err)
	}
	rows, err := dbutil.QueryRowPtrs[EvalRun](ctx, r.db,
		evalRunSelect+` WHERE run_group_id = ? ORDER BY model ASC, started_time ASC, id ASC LIMIT ? OFFSET ?`,
		runGroupID, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListByRunGroupPaginated: %w", err)
	}
	return rows, total, nil
}

type RunTarget struct {
	Name       string `db:"target_agent"`
	TargetType string `db:"target_type"`
}

const runTargetsSelect = `SELECT DISTINCT e.target_agent, e.target_type FROM eval_experiments e
	 WHERE e.target_agent IS NOT NULL AND e.target_agent != ''
	   AND EXISTS (SELECT 1 FROM eval_runs r WHERE r.experiment_id = e.id)`

func (r *EvalRunRepo) ListDistinctRunTargetsPaginated(ctx context.Context, targetType string, pageNumber, pageSize int) ([]RunTarget, int, error) {
	query, args := runTargetsSelect, []any{}
	if targetType != "" {
		query += ` AND e.target_type = ?`
		args = append(args, targetType)
	}
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM (`+query+`) targets`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListDistinctRunTargetsPaginated count: %w", err)
	}
	targets, err := dbutil.QueryRows[RunTarget](ctx, r.db,
		query+` ORDER BY e.target_agent ASC, e.target_type ASC LIMIT ? OFFSET ?`,
		append(args, pageSize, (pageNumber-1)*pageSize)...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalRunRepo.ListDistinctRunTargetsPaginated: %w", err)
	}
	return targets, total, nil
}

const evalRunSelect = `SELECT id, experiment_id, run_group_id, status, progress_current, progress_total,
	error_message, summary_scores, evaluator_stats, model, config_snapshot, evaluator_snapshot, run_name, source,
	run_as, started_by, started_time, completed_time, coordinator_started_at, takeovers
	FROM eval_runs`
