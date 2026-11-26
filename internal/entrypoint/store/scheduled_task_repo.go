// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const scheduledTaskColumns = `id, name, description, namespace, user_id, created_by,
	schedule_type, schedule_expression, timezone, target_agent_name, target_type,
	task_message, task_metadata, enabled, max_retries, retry_delay_seconds,
	timeout_seconds, source, consecutive_failure_count, run_count,
	notification_config,
	next_run_at, last_run_at, created_at, updated_at, deleted_at, deleted_by, run_as`

const executionColumns = `id, scheduled_task_id, status, a2a_task_id, scheduled_for,
	started_at, completed_at, result_summary, error_message, artifacts, notifications_sent,
	retry_count, trigger_type, triggered_by, task_snapshot, submitted_at, finalized_at, budget_stop, run_as, refusal_cause, refusal_notice`

const prefixedExecutionColumns = `e.id, e.scheduled_task_id, e.status, e.a2a_task_id, e.scheduled_for,
	e.started_at, e.completed_at, e.result_summary, e.error_message, e.artifacts, e.notifications_sent,
	e.retry_count, e.trigger_type, e.triggered_by, e.task_snapshot, e.submitted_at, e.finalized_at, e.budget_stop, e.run_as, e.refusal_cause, e.refusal_notice`

type ScheduledTaskRepo struct {
	db *DB
}

func NewScheduledTaskRepo(db *DB) *ScheduledTaskRepo {
	return &ScheduledTaskRepo{db: db}
}

func (r *ScheduledTaskRepo) Create(ctx context.Context, t *ScheduledTask) error {
	now := time.Now().UnixMilli()
	t.CreatedAt = now
	t.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (id, name, description, namespace, user_id, created_by,
			schedule_type, schedule_expression, timezone, target_agent_name, target_type,
			task_message, task_metadata, notification_config, enabled, max_retries, retry_delay_seconds,
			timeout_seconds, source, consecutive_failure_count, run_count,
			next_run_at, last_run_at, created_at, updated_at, deleted_at, deleted_by, run_as)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Description, t.Namespace, t.UserID, t.CreatedBy,
		t.ScheduleType, t.ScheduleExpression, t.Timezone, t.TargetAgentName, t.TargetType,
		t.TaskMessage, t.TaskMetadata, t.NotificationConfig, dbutil.BoolToInt(t.Enabled), t.MaxRetries, t.RetryDelaySeconds,
		t.TimeoutSeconds, t.Source, t.ConsecutiveFailureCount, t.RunCount,
		t.NextRunAt, t.LastRunAt, t.CreatedAt, t.UpdatedAt, t.DeletedAt, t.DeletedBy, t.RunAs,
	)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.Create(%s): %w", t.ID, err)
	}
	return nil
}

func (r *ScheduledTaskRepo) GetByID(ctx context.Context, id string) (*ScheduledTask, error) {
	return dbutil.QueryRow[ScheduledTask](ctx, r.db,
		`SELECT `+scheduledTaskColumns+` FROM scheduled_tasks WHERE id = ?`, id)
}

func (r *ScheduledTaskRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	fields["updated_at"] = time.Now().UnixMilli()

	setClauses := make([]byte, 0, 128)
	args := make([]any, 0, len(fields)+1)
	first := true
	for col, val := range fields {
		if !first {
			setClauses = append(setClauses, ',', ' ')
		}
		setClauses = append(setClauses, col...)
		setClauses = append(setClauses, " = ?"...)
		args = append(args, val)
		first = false
	}
	args = append(args, id)

	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET `+string(setClauses)+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.Update(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ScheduledTaskRepo) SoftDelete(ctx context.Context, id, deletedBy string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET deleted_at = ?, deleted_by = ?, updated_at = ?
		 WHERE id = ? AND deleted_at IS NULL`,
		now, deletedBy, now, id,
	)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.SoftDelete(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ScheduledTaskRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_tasks SET enabled = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		dbutil.BoolToInt(enabled), now, id,
	)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.SetEnabled(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ScheduledTaskRepo) FindByNamespace(ctx context.Context, namespace string, userID *string, enabledOnly bool, limit, offset int) ([]*ScheduledTask, error) {
	where := "WHERE namespace = ? AND deleted_at IS NULL"
	args := []any{namespace}
	if userID != nil {
		where += " AND user_id = ?"
		args = append(args, *userID)
	}
	if enabledOnly {
		where += " AND enabled = 1"
	}
	query := `SELECT ` + scheduledTaskColumns + ` FROM scheduled_tasks ` + where +
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	return dbutil.QueryRowPtrs[ScheduledTask](ctx, r.db, query, append(args, limit, offset)...)
}

func (r *ScheduledTaskRepo) CountByNamespace(ctx context.Context, namespace string, userID *string, enabledOnly bool) (int, error) {
	where := "WHERE namespace = ? AND deleted_at IS NULL"
	args := []any{namespace}
	if userID != nil {
		where += " AND user_id = ?"
		args = append(args, *userID)
	}
	if enabledOnly {
		where += " AND enabled = 1"
	}
	return dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM scheduled_tasks `+where, args...)
}

func (r *ScheduledTaskRepo) FindByNamespaceWithShared(ctx context.Context, namespace, userID string, enabledOnly bool, limit, offset int) ([]*ScheduledTask, error) {
	where := "WHERE namespace = ? AND deleted_at IS NULL AND (user_id = ? OR user_id IS NULL)"
	args := []any{namespace, userID}
	if enabledOnly {
		where += " AND enabled = 1"
	}
	query := `SELECT ` + scheduledTaskColumns + ` FROM scheduled_tasks ` + where +
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	return dbutil.QueryRowPtrs[ScheduledTask](ctx, r.db, query, append(args, limit, offset)...)
}

func (r *ScheduledTaskRepo) CountByNamespaceWithShared(ctx context.Context, namespace, userID string, enabledOnly bool) (int, error) {
	where := "WHERE namespace = ? AND deleted_at IS NULL AND (user_id = ? OR user_id IS NULL)"
	args := []any{namespace, userID}
	if enabledOnly {
		where += " AND enabled = 1"
	}
	return dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM scheduled_tasks `+where, args...)
}

func (r *ScheduledTaskRepo) FindEnabledByNamespace(ctx context.Context, namespace string) ([]*ScheduledTask, error) {
	return dbutil.QueryRowPtrs[ScheduledTask](ctx, r.db,
		`SELECT `+scheduledTaskColumns+` FROM scheduled_tasks
		 WHERE namespace = ? AND enabled = 1 AND deleted_at IS NULL
		 ORDER BY created_at ASC`, namespace)
}

func (r *ScheduledTaskRepo) CreateExecution(ctx context.Context, e *ScheduledTaskExecution) error {
	return insertExecution(ctx, r.db, e)
}

func insertExecution(ctx context.Context, q dbutil.Querier, e *ScheduledTaskExecution) error {
	if e.TriggerType == "" {
		e.TriggerType = TriggerTypeScheduled
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO scheduled_task_executions (id, scheduled_task_id, status, a2a_task_id,
			scheduled_for, started_at, completed_at, result_summary, error_message, artifacts,
			retry_count, trigger_type, triggered_by, task_snapshot, submitted_at, finalized_at, run_as)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ScheduledTaskID, e.Status, e.A2ATaskID,
		e.ScheduledFor, e.StartedAt, e.CompletedAt, e.ResultSummary, e.ErrorMessage, e.Artifacts,
		e.RetryCount, e.TriggerType, e.TriggeredBy, e.TaskSnapshot, e.SubmittedAt, e.FinalizedAt, e.RunAs,
	)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.CreateExecution(%s): %w", e.ID, err)
	}
	return nil
}

func (r *ScheduledTaskRepo) UpdateExecution(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	setClauses := make([]byte, 0, 128)
	args := make([]any, 0, len(fields)+1)
	first := true
	for col, val := range fields {
		if !first {
			setClauses = append(setClauses, ',', ' ')
		}
		setClauses = append(setClauses, col...)
		setClauses = append(setClauses, " = ?"...)
		args = append(args, val)
		first = false
	}
	args = append(args, id)

	result, err := r.db.ExecContext(ctx,
		`UPDATE scheduled_task_executions SET `+string(setClauses)+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.UpdateExecution(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ScheduledTaskRepo) GetExecutionByID(ctx context.Context, id string) (*ScheduledTaskExecution, error) {
	return dbutil.QueryRow[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+executionColumns+` FROM scheduled_task_executions WHERE id = ?`, id)
}

func (r *ScheduledTaskRepo) GetExecutionByA2ATaskID(ctx context.Context, a2aTaskID string) (*ScheduledTaskExecution, error) {
	return dbutil.QueryRow[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+executionColumns+` FROM scheduled_task_executions WHERE a2a_task_id = ?`, a2aTaskID)
}

func (r *ScheduledTaskRepo) FindExecutionsByTask(ctx context.Context, taskID string, limit, offset int, scheduledAfter, scheduledBefore *int64) ([]*ScheduledTaskExecution, error) {
	where := "WHERE scheduled_task_id = ?"
	args := []any{taskID}
	if scheduledAfter != nil {
		where += " AND scheduled_for >= ?"
		args = append(args, *scheduledAfter)
	}
	if scheduledBefore != nil {
		where += " AND scheduled_for <= ?"
		args = append(args, *scheduledBefore)
	}
	args = append(args, limit, offset)
	return dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+executionColumns+` FROM scheduled_task_executions `+where+
			` ORDER BY scheduled_for DESC, id DESC LIMIT ? OFFSET ?`,
		args...)
}

func (r *ScheduledTaskRepo) CountExecutionsByTask(ctx context.Context, taskID string, scheduledAfter, scheduledBefore *int64) (int, error) {
	where := "WHERE scheduled_task_id = ?"
	args := []any{taskID}
	if scheduledAfter != nil {
		where += " AND scheduled_for >= ?"
		args = append(args, *scheduledAfter)
	}
	if scheduledBefore != nil {
		where += " AND scheduled_for <= ?"
		args = append(args, *scheduledBefore)
	}
	return dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM scheduled_task_executions `+where, args...)
}

func (r *ScheduledTaskRepo) DeleteExecution(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM scheduled_task_executions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("ScheduledTaskRepo.DeleteExecution(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ScheduledTaskRepo) FindLastCompletedExecutionByTask(ctx context.Context, taskID string) (*ScheduledTaskExecution, error) {
	return dbutil.QueryRow[ScheduledTaskExecution](ctx, r.db,
		`SELECT `+executionColumns+` FROM scheduled_task_executions
		 WHERE scheduled_task_id = ?
		   AND status IN ('completed', 'quiet', 'failed', 'timeout', 'cancelled', 'skipped')
		 ORDER BY scheduled_for DESC, id DESC LIMIT 1`, taskID)
}

func (r *ScheduledTaskRepo) FindLastCompletedExecutionsByTasks(ctx context.Context, taskIDs []string) (map[string]*ScheduledTaskExecution, error) {
	result := make(map[string]*ScheduledTaskExecution)
	if len(taskIDs) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("ScheduledTaskRepo.FindLastCompletedExecutionsByTasks: %w", err)
	}
	query := `WITH ranked AS (
		SELECT ` + executionColumns + `,
		       ROW_NUMBER() OVER (PARTITION BY scheduled_task_id ORDER BY scheduled_for DESC, id DESC) AS rn
		FROM scheduled_task_executions
		WHERE scheduled_task_id IN (` + placeholders + `)
		  AND status IN ('completed', 'quiet', 'failed', 'timeout', 'cancelled', 'skipped')
	)
	SELECT ` + executionColumns + ` FROM ranked WHERE rn = 1`
	rows, err := dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ScheduledTaskRepo.FindLastCompletedExecutionsByTasks: %w", err)
	}
	for _, e := range rows {
		result[e.ScheduledTaskID] = e
	}
	return result, nil
}

func (r *ScheduledTaskRepo) FindRecentExecutions(ctx context.Context, namespace string, userID *string, limit int) ([]*ScheduledTaskExecution, error) {
	where := "WHERE st.namespace = ? AND st.deleted_at IS NULL"
	args := []any{namespace}
	if userID != nil {
		where += " AND st.user_id = ?"
		args = append(args, *userID)
	}
	query := `SELECT ` + prefixedExecutionColumns + `
		FROM scheduled_task_executions e
		JOIN scheduled_tasks st ON st.id = e.scheduled_task_id ` + where +
		` ORDER BY e.scheduled_for DESC LIMIT ?`
	return dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db, query, append(args, limit)...)
}

func (r *ScheduledTaskRepo) DeleteOldestExecutions(ctx context.Context, taskID string, keepCount int) (int, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM scheduled_task_executions
		 WHERE scheduled_task_id = ?
		   AND id NOT IN (
		     SELECT id FROM scheduled_task_executions
		     WHERE scheduled_task_id = ?
		     ORDER BY scheduled_for DESC
		     LIMIT ?
		   )`,
		taskID, taskID, keepCount)
	if err != nil {
		return 0, fmt.Errorf("ScheduledTaskRepo.DeleteOldestExecutions(%s): %w", taskID, err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

type SchedulerSessionTaskInfo struct {
	ExecutionID     string
	ScheduledTaskID string
	TaskName        string
}

func (r *ScheduledTaskRepo) FindTaskInfoForExecutions(ctx context.Context, executionIDs []string) ([]SchedulerSessionTaskInfo, error) {
	if len(executionIDs) == 0 {
		return []SchedulerSessionTaskInfo{}, nil
	}
	placeholders := make([]string, len(executionIDs))
	args := make([]any, len(executionIDs))
	for i, id := range executionIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT e.id, e.scheduled_task_id, st.name
		FROM scheduled_task_executions e
		JOIN scheduled_tasks st ON st.id = e.scheduled_task_id
		WHERE e.id IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ScheduledTaskRepo.FindTaskInfoForExecutions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]SchedulerSessionTaskInfo, 0, len(executionIDs))
	for rows.Next() {
		var info SchedulerSessionTaskInfo
		if err := rows.Scan(&info.ExecutionID, &info.ScheduledTaskID, &info.TaskName); err != nil {
			return nil, fmt.Errorf("ScheduledTaskRepo.FindTaskInfoForExecutions scan: %w", err)
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ScheduledTaskRepo.FindTaskInfoForExecutions rows: %w", err)
	}
	return out, nil
}

func (r *ScheduledTaskRepo) FindLastExecutionsByTasks(ctx context.Context, taskIDs []string) (map[string]*ScheduledTaskExecution, error) {
	result := make(map[string]*ScheduledTaskExecution)
	if len(taskIDs) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("ScheduledTaskRepo.FindLastExecutionsByTasks: %w", err)
	}
	query := `WITH ranked AS (
		SELECT ` + executionColumns + `,
		       ROW_NUMBER() OVER (PARTITION BY scheduled_task_id ORDER BY scheduled_for DESC, id DESC) AS rn
		FROM scheduled_task_executions
		WHERE scheduled_task_id IN (` + placeholders + `)
	)
	SELECT ` + executionColumns + ` FROM ranked WHERE rn = 1`
	rows, err := dbutil.QueryRowPtrs[ScheduledTaskExecution](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ScheduledTaskRepo.FindLastExecutionsByTasks: %w", err)
	}
	for _, e := range rows {
		result[e.ScheduledTaskID] = e
	}
	return result, nil
}
