// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const thisVersionRun = `(e.submitted_at IS NULL OR e.submitted_at > 0)`

const passBatch = 100

const (
	activeExecutionStatuses   = `('pending', 'running')`
	terminalExecutionStatuses = `('completed', 'quiet', 'failed', 'timeout', 'cancelled', 'skipped')`
)

func (r *ScheduledTaskRepo) FindDueSchedules(ctx context.Context, namespace string, nowMs int64) ([]*ScheduledTask, error) {
	return dbutil.QueryRowPtrs[ScheduledTask](ctx, r.db,
		`SELECT `+scheduledTaskColumns+` FROM scheduled_tasks
		 WHERE namespace = ? AND enabled = 1 AND deleted_at IS NULL AND next_run_at <= ?
		 ORDER BY next_run_at ASC, id ASC LIMIT ?`, namespace, nowMs, passBatch)
}

func (r *ScheduledTaskRepo) FindNeverFiredSchedules(ctx context.Context, namespace string) ([]*ScheduledTask, error) {
	return dbutil.QueryRowPtrs[ScheduledTask](ctx, r.db,
		`SELECT `+scheduledTaskColumns+` FROM scheduled_tasks
		 WHERE namespace = ? AND enabled = 1 AND deleted_at IS NULL
		   AND next_run_at IS NULL AND last_run_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM scheduled_task_executions e WHERE e.scheduled_task_id = scheduled_tasks.id)
		 ORDER BY created_at ASC, id ASC LIMIT ?`, namespace, passBatch)
}

func (r *ScheduledTaskRepo) SetFirstRun(ctx context.Context, id string, nextRunMs int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET next_run_at = ?
		 WHERE id = ? AND deleted_at IS NULL AND next_run_at IS NULL AND last_run_at IS NULL`,
		nextRunMs, id)
	if err != nil {
		return false, fmt.Errorf("ScheduledTaskRepo.SetFirstRun(%s): %w", id, err)
	}
	return oneRowAffected(result, "SetFirstRun", id)
}

func (r *ScheduledTaskRepo) FireDue(ctx context.Context, id string, dueMs int64, nextRunMs *int64, nowMs int64, exec, ifBusy *ScheduledTaskExecution) (*ScheduledTaskExecution, bool, error) {
	var inserted *ScheduledTaskExecution
	won, err := dbutil.WithTxVal(ctx, r.db.DB, func(tx *dbutil.Tx) (bool, error) {
		result, err := tx.ExecContext(ctx,
			`UPDATE scheduled_tasks SET next_run_at = ?
			 WHERE id = ? AND enabled = 1 AND deleted_at IS NULL AND next_run_at = ?`,
			nextRunMs, id, dueMs)
		if err != nil {
			return false, fmt.Errorf("ScheduledTaskRepo.FireDue(%s): %w", id, err)
		}
		won, err := oneRowAffected(result, "FireDue", id)
		if err != nil || !won || exec == nil {
			return won, err
		}
		inserted = exec
		if exec.Status == "pending" {
			busy, err := hasActiveExecution(ctx, tx, id)
			if err != nil {
				return false, err
			}
			if busy && ifBusy != nil {
				inserted = ifBusy
			}
		}
		if err := insertExecution(ctx, tx, inserted); err != nil {
			return false, err
		}
		if inserted.Status != "pending" {
			return true, nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET last_run_at = ? WHERE id = ?`, nowMs, id); err != nil {
			return false, fmt.Errorf("ScheduledTaskRepo.FireDue(%s) last_run_at: %w", id, err)
		}
		return true, nil
	})
	if err != nil || !won {
		inserted = nil
	}
	return inserted, won, err
}

func (r *ScheduledTaskRepo) HasActiveExecution(ctx context.Context, taskID string) (bool, error) {
	return hasActiveExecution(ctx, r.db, taskID)
}

func hasActiveExecution(ctx context.Context, q dbutil.Querier, taskID string) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, q,
		`SELECT COUNT(*) FROM scheduled_task_executions e
		 WHERE e.scheduled_task_id = ? AND e.status IN `+activeExecutionStatuses+` AND `+thisVersionRun, taskID)
	if err != nil {
		return false, fmt.Errorf("ScheduledTaskRepo.HasActiveExecution(%s): %w", taskID, err)
	}
	return n > 0, nil
}

func (r *ScheduledTaskRepo) HasUnsentExecutionNoted(ctx context.Context, taskID string, notes ...string) (bool, error) {
	if len(notes) == 0 {
		return false, nil
	}
	args := []any{taskID}
	for _, note := range notes {
		args = append(args, note)
	}
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM scheduled_task_executions
		 WHERE scheduled_task_id = ? AND status = 'pending' AND submitted_at IS NULL
		   AND error_message IN (`+strings.TrimSuffix(strings.Repeat("?,", len(notes)), ",")+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("ScheduledTaskRepo.HasUnsentExecutionNoted(%s): %w", taskID, err)
	}
	return n > 0, nil
}

func (r *ScheduledTaskRepo) CreateExecutionIfIdle(ctx context.Context, exec *ScheduledTaskExecution) (busy bool, err error) {
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, lockErr := tx.ExecContext(ctx, `UPDATE scheduled_tasks SET updated_at = updated_at WHERE id = ?`, exec.ScheduledTaskID); lockErr != nil {
			return fmt.Errorf("ScheduledTaskRepo.CreateExecutionIfIdle(%s) lock: %w", exec.ScheduledTaskID, lockErr)
		}
		running, queryErr := dbutil.QueryVal[int](ctx, tx,
			`SELECT COUNT(*) FROM scheduled_task_executions e
			 WHERE e.scheduled_task_id = ? AND e.status IN `+activeExecutionStatuses+` AND `+thisVersionRun, exec.ScheduledTaskID)
		if queryErr != nil {
			return fmt.Errorf("ScheduledTaskRepo.CreateExecutionIfIdle(%s) active: %w", exec.ScheduledTaskID, queryErr)
		}
		if busy = running > 0; busy {
			return nil
		}
		return insertExecution(ctx, tx, exec)
	})
	return busy, err
}

func (r *ScheduledTaskRepo) FindUnsubmittedExecutions(ctx context.Context, namespace string, nowMs int64) ([]*ScheduledTaskExecution, error) {
	return dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+prefixedExecutionColumns+` FROM scheduled_task_executions e
		 JOIN scheduled_tasks st ON st.id = e.scheduled_task_id
		 WHERE st.namespace = ? AND e.status = 'pending' AND e.submitted_at IS NULL
		   AND e.scheduled_for <= ?
		 ORDER BY e.scheduled_for ASC, e.id ASC LIMIT ?`, namespace, nowMs, passBatch)
}

func (r *ScheduledTaskRepo) MarkSubmitted(ctx context.Context, id string, nowMs int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_task_executions SET submitted_at = ?, status = 'running', started_at = ?, error_message = NULL
		 WHERE id = ? AND status = 'pending' AND submitted_at IS NULL`,
		nowMs, nowMs, id)
	if err != nil {
		return false, fmt.Errorf("ScheduledTaskRepo.MarkSubmitted(%s): %w", id, err)
	}
	return oneRowAffected(result, "MarkSubmitted", id)
}

func (r *ScheduledTaskRepo) NoteUnsent(ctx context.Context, id string, note *string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_task_executions SET error_message = ?
		 WHERE id = ? AND status = 'pending' AND submitted_at IS NULL`,
		note, id); err != nil {
		return fmt.Errorf("ScheduledTaskRepo.NoteUnsent(%s): %w", id, err)
	}
	return nil
}

type ExecutionEnd struct {
	Status        string
	CompletedAt   int64
	ResultSummary *string
	Artifacts     *string
	ErrorMessage  *string
	Answered      bool
	Cause         EndCause
}

type EndCause int

const (
	EndByRun EndCause = iota
	EndBudgetUnconfirmed
	EndBudgetSpent
	EndRunAsDisabled
	EndPlatformTooOld
	EndIdentityUnconfirmed
)

func (c EndCause) uncounted() bool {
	return c == EndBudgetUnconfirmed || c.refusal() != "" || c == EndIdentityUnconfirmed
}

func (c EndCause) refusal() string {
	switch c {
	case EndRunAsDisabled:
		return RefusalRunAsDisabled
	case EndPlatformTooOld:
		return RefusalPlatformTooOld
	}
	return ""
}

type RunEnd struct {
	ExecutionID    string
	TaskID         string
	FromStatus     string
	End            ExecutionEnd
	AutoPauseAfter int
	Retry          *ScheduledTaskExecution
}

type RunEndResult struct {
	Ended               bool
	Counted             bool
	Paused              bool
	ConsecutiveFailures int
	BudgetStop          string
}

func (r *ScheduledTaskRepo) EndExecution(ctx context.Context, e RunEnd) (RunEndResult, error) {
	return dbutil.WithTxVal(ctx, r.db.DB, func(tx *dbutil.Tx) (RunEndResult, error) {
		var res RunEndResult
		end := e.End
		query := `UPDATE scheduled_task_executions
			 SET status = ?, completed_at = ?, result_summary = ?, artifacts = ?, error_message = ?
			 WHERE id = ? AND status = ?`
		args := []any{end.Status, end.CompletedAt, end.ResultSummary, end.Artifacts, end.ErrorMessage, e.ExecutionID, e.FromStatus}
		if end.Answered {
			query = `UPDATE scheduled_task_executions
			 SET status = ?, completed_at = ?, result_summary = ?, artifacts = ?, error_message = ?,
			     submitted_at = COALESCE(submitted_at, ?)
			 WHERE id = ? AND status IN ` + activeExecutionStatuses
			args = []any{end.Status, end.CompletedAt, end.ResultSummary, end.Artifacts, end.ErrorMessage, end.CompletedAt, e.ExecutionID}
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return res, fmt.Errorf("ScheduledTaskRepo.EndExecution(%s): %w", e.ExecutionID, err)
		}
		if res.Ended, err = oneRowAffected(result, "EndExecution", e.ExecutionID); err != nil || !res.Ended {
			return res, err
		}
		owed, err := dbutil.QueryVal[int](ctx, tx,
			`SELECT COUNT(*) FROM scheduled_task_executions WHERE id = ? AND finalized_at IS NULL`, e.ExecutionID)
		if err != nil {
			return res, fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) read finalized_at: %w", e.ExecutionID, err)
		}
		if refusal := end.Cause.refusal(); refusal != "" {
			err = recordRefusalNotice(ctx, tx, e.ExecutionID, refusal)
			if err != nil {
				return res, err
			}
		}
		switch {
		case owed == 0 || end.Cause.uncounted():
			return res, nil
		case end.Cause == EndBudgetSpent:
			res.BudgetStop, err = recordBudgetStop(ctx, tx, e)
			return res, err
		}
		res.Counted = true
		err = applyRunEnd(ctx, tx, e, &res)
		return res, err
	})
}

func recordRefusalNotice(ctx context.Context, tx *dbutil.Tx, executionID, refusal string) error {
	run, err := dbutil.QueryRow[ScheduledTaskExecution](ctx, tx,
		`SELECT `+executionColumns+` FROM scheduled_task_executions WHERE id = ?`, executionID)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) read refused run: %w", executionID, err)
	}
	if run == nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) read refused run: no such run", executionID)
	}
	notice := RefusalNoticeFirst
	if run.TriggerType == TriggerTypeScheduled {
		previous, err := previousStreakRun(ctx, tx, run)
		if err != nil {
			return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) read previous run: %w", executionID, err)
		}
		if continuesRefusalStreak(previous, refusal) {
			notice = RefusalNoticeRepeat
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE scheduled_task_executions SET refusal_cause = ?, refusal_notice = ? WHERE id = ?`,
		refusal, notice, executionID); err != nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) refusal notice: %w", executionID, err)
	}
	return nil
}

func previousStreakRun(ctx context.Context, q dbutil.Querier, run *ScheduledTaskExecution) (*ScheduledTaskExecution, error) {
	return dbutil.QueryRow[ScheduledTaskExecution](ctx, q,
		`SELECT `+executionColumns+` FROM scheduled_task_executions
		 WHERE scheduled_task_id = ?
		   AND status IN `+terminalExecutionStatuses+`
		   AND (scheduled_for < ? OR (scheduled_for = ? AND id < ?))
		   AND NOT (status = 'skipped' AND COALESCE(error_message, '') IN (?, ?))
		   AND NOT (trigger_type = ? AND refusal_cause IS NOT NULL)
		 ORDER BY scheduled_for DESC, id DESC LIMIT 1`,
		run.ScheduledTaskID, run.ScheduledFor, run.ScheduledFor, run.ID,
		SkippedRunningNote, SkippedIdentityWaitNote, TriggerTypeManual)
}

func continuesRefusalStreak(previous *ScheduledTaskExecution, refusal string) bool {
	if previous == nil || previous.RefusalCause == nil || *previous.RefusalCause != refusal || previous.RefusalNotice == nil {
		return false
	}
	switch *previous.RefusalNotice {
	case RefusalNoticeRepeat:
		return true
	case RefusalNoticeFirst:
		return previous.NoticeDelivered()
	}
	return false
}

func recordBudgetStop(ctx context.Context, tx *dbutil.Tx, e RunEnd) (string, error) {
	result, err := tx.ExecContext(ctx,
		`UPDATE scheduled_tasks SET budget_blocked_at = ?
		 WHERE id = ? AND (budget_blocked_at IS NULL OR EXISTS (
		   SELECT 1 FROM scheduled_task_executions WHERE id = ? AND submitted_at IS NOT NULL))`,
		e.End.CompletedAt, e.TaskID, e.ExecutionID)
	if err != nil {
		return "", fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) budget streak: %w", e.ExecutionID, err)
	}
	started, err := oneRowAffected(result, "EndExecution", e.ExecutionID)
	if err != nil {
		return "", err
	}
	stop := BudgetStopRepeat
	if started {
		stop = BudgetStopFirst
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE scheduled_task_executions SET budget_stop = ? WHERE id = ?`, stop, e.ExecutionID); err != nil {
		return "", fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) budget stop: %w", e.ExecutionID, err)
	}
	return stop, nil
}

func applyRunEnd(ctx context.Context, tx *dbutil.Tx, e RunEnd, res *RunEndResult) error {
	succeeded := e.End.Status == "completed" || e.End.Status == ExecutionStatusQuiet
	counters := `UPDATE scheduled_tasks SET consecutive_failure_count = consecutive_failure_count + 1,
		last_run_at = ?, updated_at = ? WHERE id = ?`
	if succeeded {
		counters = `UPDATE scheduled_tasks SET run_count = run_count + 1, consecutive_failure_count = 0,
			last_run_at = ?, updated_at = ? WHERE id = ?`
	}
	if _, err := tx.ExecContext(ctx, counters, e.End.CompletedAt, e.End.CompletedAt, e.TaskID); err != nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) counters: %w", e.ExecutionID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE scheduled_tasks SET budget_blocked_at = NULL
		 WHERE id = ? AND EXISTS (SELECT 1 FROM scheduled_task_executions WHERE id = ? AND submitted_at IS NOT NULL)`,
		e.TaskID, e.ExecutionID); err != nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) end budget streak: %w", e.ExecutionID, err)
	}
	if e.Retry != nil {
		return insertExecution(ctx, tx, e.Retry)
	}
	if succeeded || e.AutoPauseAfter <= 0 {
		return nil
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE scheduled_tasks SET enabled = 0, next_run_at = NULL, updated_at = ?
		 WHERE id = ? AND enabled = 1 AND deleted_at IS NULL AND consecutive_failure_count >= ?`,
		e.End.CompletedAt, e.TaskID, e.AutoPauseAfter)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) auto-pause: %w", e.ExecutionID, err)
	}
	if res.Paused, err = oneRowAffected(result, "EndExecution", e.ExecutionID); err != nil || !res.Paused {
		return err
	}
	res.ConsecutiveFailures, err = dbutil.QueryVal[int](ctx, tx,
		`SELECT consecutive_failure_count FROM scheduled_tasks WHERE id = ?`, e.TaskID)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.EndExecution(%s) read failures: %w", e.ExecutionID, err)
	}
	return nil
}

func (r *ScheduledTaskRepo) FindOverdueExecutions(ctx context.Context, namespace string, nowMs, defaultTimeoutMs int64) ([]*ScheduledTaskExecution, error) {
	return dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+prefixedExecutionColumns+` FROM scheduled_task_executions e
		 JOIN scheduled_tasks st ON st.id = e.scheduled_task_id
		 WHERE st.namespace = ?
		   AND ((e.status = 'running' AND COALESCE(e.started_at, e.scheduled_for) + `+timeoutMsExpr+` < ?)
		     OR (e.status = 'pending' AND e.scheduled_for + `+timeoutMsExpr+` < ?))
		 ORDER BY e.scheduled_for ASC, e.id ASC LIMIT ?`,
		namespace, defaultTimeoutMs, nowMs, defaultTimeoutMs, nowMs, passBatch)
}

const timeoutMsExpr = `(CASE WHEN st.timeout_seconds > 0
	THEN CAST(st.timeout_seconds AS BIGINT) * 1000 ELSE CAST(? AS BIGINT) END)`

func (r *ScheduledTaskRepo) CountOpenExecutions(ctx context.Context, namespace string) (running, pending int, err error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN e.status = 'running' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN e.status = 'pending' THEN 1 ELSE 0 END), 0)
		 FROM scheduled_task_executions e
		 JOIN scheduled_tasks st ON st.id = e.scheduled_task_id
		 WHERE st.namespace = ? AND e.status IN `+activeExecutionStatuses, namespace)
	if err := row.Scan(&running, &pending); err != nil {
		return 0, 0, fmt.Errorf("ScheduledTaskRepo.CountOpenExecutions(%s): %w", namespace, err)
	}
	return running, pending, nil
}

func (r *ScheduledTaskRepo) FindUnfinalizedExecutions(ctx context.Context, namespace string) ([]*ScheduledTaskExecution, error) {
	return dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+prefixedExecutionColumns+` FROM scheduled_task_executions e
		 JOIN scheduled_tasks st ON st.id = e.scheduled_task_id
		 WHERE st.namespace = ? AND e.finalized_at IS NULL AND e.status IN `+terminalExecutionStatuses+`
		 ORDER BY CASE WHEN e.budget_stop = ? AND e.notifications_sent IS NOT NULL THEN 1 ELSE 0 END,
		          e.scheduled_for ASC, e.id ASC LIMIT ?`, namespace, BudgetStopFirst, passBatch)
}

func (r *ScheduledTaskRepo) ClaimFinalize(ctx context.Context, id string, nowMs int64) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_task_executions SET finalized_at = ?
		 WHERE id = ? AND finalized_at IS NULL AND status IN `+terminalExecutionStatuses,
		nowMs, id)
	if err != nil {
		return false, fmt.Errorf("ScheduledTaskRepo.ClaimFinalize(%s): %w", id, err)
	}
	return oneRowAffected(result, "ClaimFinalize", id)
}

func oneRowAffected(result interface{ RowsAffected() (int64, error) }, op, id string) (bool, error) {
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("ScheduledTaskRepo.%s(%s) rows affected: %w", op, id, err)
	}
	return n == 1, nil
}
