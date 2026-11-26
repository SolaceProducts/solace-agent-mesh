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
	"github.com/google/uuid"
)

type TaskRepo struct {
	db *DB
}

func NewTaskRepo(db *DB) *TaskRepo {
	return &TaskRepo{db: db}
}

func (r *TaskRepo) Create(ctx context.Context, t *Task) error {
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, user_id, parent_task_id, start_time, end_time, status,
				initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
				token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
				max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens,
				agent_name, error_message, handler_name, handler_type, trigger_kind,
				channel, entrypoint_id, entrypoint_name, trigger_source)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.UserID, t.ParentTaskID, t.StartTime, t.EndTime, t.Status,
			t.InitialRequestText, t.TotalInputTokens, t.TotalOutputTokens, t.TotalCachedInputTokens,
			t.TokenUsageDetails, t.ExecutionMode, t.LastActivityTime, dbutil.BoolToInt(t.BackgroundExecutionEnabled),
			t.MaxExecutionTimeMs, t.SessionID, dbutil.BoolToInt(t.EventsBuffered), dbutil.BoolToInt(t.EventsConsumed),
			taskKindOrDefault(t.Kind), t.PostCompactionRemainingTokens,
			t.AgentName, t.ErrorMessage, t.HandlerName, t.HandlerType, t.TriggerKind,
			t.Channel, t.EntrypointID, t.EntrypointName, t.TriggerSource,
		); err != nil {
			return err
		}
		if t.SessionID == nil || *t.SessionID == "" {
			return nil
		}
		return markSessionActiveTx(ctx, tx, *t.SessionID)
	})
	if err != nil {
		return fmt.Errorf("TaskRepo.Create(%s): %w", t.ID, err)
	}
	return nil
}

func (r *TaskRepo) GetByID(ctx context.Context, id string) (*Task, error) {
	return dbutil.QueryRow[Task](ctx, r.db,
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type,
			`+taskTriggerColumns+`
		 FROM tasks WHERE id = ?`, id)
}

const taskTriggerColumns = `trigger_kind, channel, entrypoint_id, entrypoint_name, trigger_source`

func (r *TaskRepo) FillClassificationFromRequest(ctx context.Context, id string, from *Task) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET
			handler_name    = COALESCE(handler_name, ?),
			handler_type    = COALESCE(handler_type, ?),
			parent_task_id  = COALESCE(parent_task_id, ?),
			agent_name      = COALESCE(agent_name, ?),
			trigger_kind    = COALESCE(trigger_kind, ?),
			channel         = COALESCE(channel, ?),
			entrypoint_id   = COALESCE(entrypoint_id, ?),
			entrypoint_name = COALESCE(entrypoint_name, ?),
			trigger_source  = COALESCE(trigger_source, ?)
		 WHERE id = ?`,
		from.HandlerName, from.HandlerType, from.ParentTaskID, from.AgentName, from.TriggerKind,
		from.Channel, from.EntrypointID, from.EntrypointName, from.TriggerSource,
		id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.FillClassificationFromRequest(%s): %w", id, err)
	}
	return nil
}

func (r *TaskRepo) UpdateStatus(ctx context.Context, id, status string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, last_activity_time = ? WHERE id = ?`,
		status, now, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.UpdateStatus(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *TaskRepo) UpdateEndTime(ctx context.Context, id string, endTime int64) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET end_time = ?, last_activity_time = ? WHERE id = ?`,
		endTime, endTime, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.UpdateEndTime(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *TaskRepo) UpdateTokenUsage(ctx context.Context, id string, input, output, cached int) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET total_input_tokens = ?, total_output_tokens = ?, total_cached_input_tokens = ? WHERE id = ?`,
		input, output, cached, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.UpdateTokenUsage(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *TaskRepo) ListByUser(ctx context.Context, userID string, executionMode string, limit, offset int) ([]*Task, error) {
	where := "WHERE user_id = ?"
	args := []any{userID}
	if executionMode != "" {
		where += " AND execution_mode = ?"
		args = append(args, executionMode)
	}

	query := `SELECT id, user_id, parent_task_id, start_time, end_time, status,
		initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
		token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
		max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		FROM tasks ` + where + ` ORDER BY start_time DESC, id DESC LIMIT ? OFFSET ?`

	return dbutil.QueryRowPtrs[Task](ctx, r.db, query, append(args, limit, offset)...)
}

func (r *TaskRepo) ListActiveBackground(ctx context.Context, userID string) ([]*Task, error) {
	return dbutil.QueryRowPtrs[Task](ctx, r.db,
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		 FROM tasks
		 WHERE user_id = ? AND execution_mode = 'background' AND end_time IS NULL
		 ORDER BY start_time DESC`,
		userID)
}

type TaskSortField string

const (
	TaskSortStartTime    TaskSortField = "start_time"
	TaskSortLastActivity TaskSortField = "last_activity_time"
)

type TaskSortDir string

const (
	TaskSortDesc TaskSortDir = "DESC"
	TaskSortAsc  TaskSortDir = "ASC"
)

type TaskSearchOpts struct {
	StartDate  int64
	EndDate    int64
	Kind       string
	UserID     string
	SessionIDs []string
	Search     string
	SortField  TaskSortField
	SortDir    TaskSortDir
}

type TaskVisibility struct {
	OwnerUserID       string
	Everything        bool
	AllAgentRoots     bool
	AgentRootHandlers []string
}

const matchesNoRow = "1 = 0"

func (v TaskVisibility) whereClause() (string, []any, error) {
	if v.Everything {
		return "", nil, nil
	}
	var clauses []string
	var args []any
	if v.OwnerUserID != "" {
		clauses = append(clauses, "user_id = ?")
		args = append(args, v.OwnerUserID)
	}
	switch {
	case v.AllAgentRoots:
		clauses = append(clauses, "(parent_task_id IS NULL AND handler_type = ? AND handler_name IS NOT NULL AND handler_name <> '')")
		args = append(args, TaskHandlerTypeAgent)
	case len(v.AgentRootHandlers) > 0:
		placeholders, names, err := dbutil.InClause(v.AgentRootHandlers)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "(parent_task_id IS NULL AND handler_type = ? AND handler_name IN ("+placeholders+"))")
		args = append(append(args, TaskHandlerTypeAgent), names...)
	}
	if len(clauses) == 0 {
		return matchesNoRow, nil, nil
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args, nil
}

func (r *TaskRepo) Search(ctx context.Context, vis TaskVisibility, opts TaskSearchOpts, limit, offset int) ([]*Task, int, error) {
	clause, args, err := vis.whereClause()
	if err != nil {
		return nil, 0, fmt.Errorf("TaskRepo.Search: %w", err)
	}
	var conditions []string
	if clause != "" {
		conditions = append(conditions, clause)
	}
	if opts.Kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, opts.Kind)
	}
	if opts.StartDate > 0 {
		conditions = append(conditions, "start_time >= ?")
		args = append(args, opts.StartDate)
	}
	if opts.EndDate > 0 {
		conditions = append(conditions, "start_time <= ?")
		args = append(args, opts.EndDate)
	}
	if opts.UserID != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, opts.UserID)
	}
	if len(opts.SessionIDs) > 0 {
		sessionIDs := canonicalUUIDs(opts.SessionIDs)
		if len(sessionIDs) == 0 {
			conditions = append(conditions, matchesNoRow)
		} else {
			ph, inArgs, err := dbutil.InClause(sessionIDs)
			if err != nil {
				return nil, 0, fmt.Errorf("TaskRepo.Search: %w", err)
			}
			conditions = append(conditions, "session_id IN ("+ph+")")
			args = append(args, inArgs...)
		}
	}
	if opts.Search != "" {
		clause, searchArgs := taskSearchCondition(opts.Search)
		conditions = append(conditions, clause)
		args = append(args, searchArgs...)
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	sortField := opts.SortField
	if sortField == "" {
		sortField = TaskSortStartTime
	}
	sortDir := opts.SortDir
	if sortDir == "" {
		sortDir = TaskSortDesc
	}

	return listPageWithDerivedTotal[Task](ctx, r.db,
		`id, user_id, parent_task_id, start_time, end_time, status,
		initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
		token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
		max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type,
		`+taskTriggerColumns,
		"tasks", where, string(sortField)+` `+string(sortDir)+`, id DESC`,
		args, limit, offset, "TaskRepo.Search")
}

func (r *TaskRepo) FindActiveChildren(ctx context.Context, parentTaskID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM tasks
		 WHERE parent_task_id = ?
		   AND (status IS NULL OR status IN ('running', 'pending'))
		   AND end_time IS NULL`,
		parentTaskID)
}

func (r *TaskRepo) HasActiveBackgroundTask(ctx context.Context, sessionID string) (bool, error) {
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM tasks
		 WHERE session_id = ? AND execution_mode = 'background' AND end_time IS NULL`,
		sessionID)
	if err != nil {
		return false, fmt.Errorf("TaskRepo.HasActiveBackgroundTask(%s): %w", sessionID, err)
	}
	return count > 0, nil
}

func (r *TaskRepo) GetBySessionID(ctx context.Context, sessionID string) (*Task, error) {
	return dbutil.QueryRow[Task](ctx, r.db,
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		 FROM tasks WHERE session_id = ? ORDER BY start_time DESC LIMIT 1`, sessionID)
}

func (r *TaskRepo) ListSessionRootsUpTo(ctx context.Context, sessionID, ratedRootID string, upToStartTime int64, limit int) ([]*Task, error) {
	var rated any
	if ratedRootID != "" {
		rated = ratedRootID
	}
	return dbutil.QueryRowPtrs[Task](ctx, r.db,
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		 FROM tasks
		 WHERE session_id = ? AND parent_task_id IS NULL AND kind = ? AND start_time <= ?
		   AND ((handler_name IS NOT NULL AND handler_name <> '') OR id = ?)
		 ORDER BY start_time DESC
		 LIMIT ?`, sessionID, TaskKindNormal, upToStartTime, rated, limit)
}

func (r *TaskRepo) ListBySessionForUserWithTokens(ctx context.Context, sessionID, userID string) ([]*Task, error) {
	return dbutil.QueryRowPtrs[Task](ctx, r.db,
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		 FROM tasks
		 WHERE session_id = ? AND user_id = ? AND total_input_tokens IS NOT NULL
			AND parent_task_id IS NULL
		 ORDER BY start_time DESC`, sessionID, userID)
}

func (r *TaskRepo) SumTokensBySessionForUser(ctx context.Context, sessionID, userID, excludeTaskID string) (inputTokens, outputTokens int, err error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(total_input_tokens), 0), COALESCE(SUM(total_output_tokens), 0)
		 FROM tasks
		 WHERE session_id = ? AND user_id = ? AND total_input_tokens IS NOT NULL
		   AND parent_task_id IS NULL AND id <> ?`,
		sessionID, userID, excludeTaskID)
	if err = row.Scan(&inputTokens, &outputTokens); err != nil {
		return 0, 0, fmt.Errorf("TaskRepo.SumTokensBySessionForUser(%s): %w", sessionID, err)
	}
	return inputTokens, outputTokens, nil
}

func (r *TaskRepo) GetByIDs(ctx context.Context, ids []string) ([]*Task, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.GetByIDs: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		 FROM tasks WHERE id IN (%s)`, ph,
	)
	return dbutil.QueryRowPtrs[Task](ctx, r.db, query, args...)
}

func (r *TaskRepo) HasActiveBackgroundTaskBatch(ctx context.Context, sessionIDs []string) (map[string]bool, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.HasActiveBackgroundTaskBatch: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT session_id FROM tasks
		 WHERE session_id IN (%s) AND execution_mode = 'background' AND end_time IS NULL
		 GROUP BY session_id`, ph,
	)
	sids, err := dbutil.QueryVals[string](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.HasActiveBackgroundTaskBatch: %w", err)
	}
	result := make(map[string]bool, len(sessionIDs))
	for _, sid := range sids {
		result[sid] = true
	}
	return result, nil
}

func (r *TaskRepo) GetParentChain(ctx context.Context, taskID string, maxDepth int) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`WITH RECURSIVE chain AS (
			SELECT id, parent_task_id, 0 AS depth FROM tasks WHERE id = ?
			UNION ALL
			SELECT t.id, t.parent_task_id, c.depth + 1
			FROM tasks t JOIN chain c ON t.id = c.parent_task_id
			WHERE c.depth < ?
		)
		SELECT id FROM chain ORDER BY depth ASC`, taskID, maxDepth)
}

func (r *TaskRepo) GetChildTaskIDs(ctx context.Context, parentTaskID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM tasks WHERE parent_task_id = ? ORDER BY start_time ASC`,
		parentTaskID)
}

type FinalStatusUpdate struct {
	Status            string
	EndTime           int64
	InputTokens       *int
	OutputTokens      *int
	CachedInputTokens *int
	TokenUsageDetails *string
	ErrorMessage      *string
}

func (r *TaskRepo) UpdateFinalStatus(ctx context.Context, id string, u FinalStatusUpdate) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, end_time = ?, last_activity_time = ?,
			total_input_tokens        = COALESCE(?, total_input_tokens),
			total_output_tokens       = COALESCE(?, total_output_tokens),
			total_cached_input_tokens = COALESCE(?, total_cached_input_tokens),
			token_usage_details       = COALESCE(?, token_usage_details),
			error_message             = ?
		 WHERE id = ?`,
		u.Status, u.EndTime, u.EndTime,
		u.InputTokens, u.OutputTokens, u.CachedInputTokens,
		u.TokenUsageDetails, u.ErrorMessage, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.UpdateFinalStatus(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

type TokenUsageFill struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	TokenUsageDetails *string
}

func (r *TaskRepo) FillTokenUsage(ctx context.Context, id string, u TokenUsageFill) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET total_input_tokens = ?, total_output_tokens = ?,
			total_cached_input_tokens = ?, token_usage_details = ?
		 WHERE id = ? AND total_input_tokens IS NULL`,
		u.InputTokens, u.OutputTokens, u.CachedInputTokens, u.TokenUsageDetails, id,
	)
	if err != nil {
		return false, fmt.Errorf("TaskRepo.FillTokenUsage(%s): %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("TaskRepo.FillTokenUsage(%s) rows affected: %w", id, err)
	}
	return rows > 0, nil
}

func (r *TaskRepo) UpdateLastActivityTime(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET last_activity_time = ? WHERE id = ?`, now, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.UpdateLastActivityTime(%s): %w", id, err)
	}
	return nil
}

func (r *TaskRepo) MarkEventsBuffered(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET events_buffered = 1 WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.MarkEventsBuffered(%s): %w", id, err)
	}
	return nil
}

func (r *TaskRepo) MarkEventsBufferedBatch(ctx context.Context, ids []string) error {
	for start := 0; start < len(ids); start += dbutil.InClauseCap {
		in, args, err := dbutil.InClause(ids[start:min(start+dbutil.InClauseCap, len(ids))])
		if err != nil {
			return fmt.Errorf("TaskRepo.MarkEventsBufferedBatch: %w", err)
		}
		if _, err := r.db.ExecContext(ctx, `UPDATE tasks SET events_buffered = 1 WHERE id IN (`+in+`)`, args...); err != nil {
			return fmt.Errorf("TaskRepo.MarkEventsBufferedBatch(%d tasks): %w", len(ids), err)
		}
	}
	return nil
}

func (r *TaskRepo) MarkEventsConsumed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET events_consumed = 1 WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("TaskRepo.MarkEventsConsumed(%s): %w", id, err)
	}
	return nil
}

const TaskTreeMaxDepth = 50

type descendantRow struct {
	ID    string `db:"id"`
	Depth int    `db:"depth"`
}

func (r *TaskRepo) DescendantIDs(ctx context.Context, taskID string) (ids []string, atBound bool, err error) {
	rows, err := dbutil.QueryRowPtrs[descendantRow](ctx, r.db,
		`WITH RECURSIVE descendants AS (
			SELECT id, 1 AS depth FROM tasks WHERE parent_task_id = ?
			UNION ALL
			SELECT t.id, d.depth + 1 FROM tasks t JOIN descendants d ON t.parent_task_id = d.id
			WHERE d.depth < ?
		)
		SELECT id, MAX(depth) AS depth FROM descendants GROUP BY id`, taskID, TaskTreeMaxDepth)
	if err != nil {
		return nil, false, fmt.Errorf("TaskRepo.DescendantIDs(%s): %w", taskID, err)
	}
	ids = make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		if row.Depth >= TaskTreeMaxDepth {
			atBound = true
		}
	}
	return ids, atBound, nil
}

type ForestNode struct {
	ID          string `db:"id"`
	RootID      string `db:"root_id"`
	Depth       int    `db:"depth"`
	UserID      string `db:"user_id"`
	HandlerName string `db:"handler_name"`
	HandlerType string `db:"handler_type"`
	StartTime   int64  `db:"start_time"`
}

func (r *TaskRepo) SessionForest(ctx context.Context, rootIDs []string) ([]ForestNode, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(rootIDs)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.SessionForest: %w", err)
	}
	args = append(args, TaskTreeMaxDepth)
	rows, err := dbutil.QueryRowPtrs[ForestNode](ctx, r.db,
		`WITH RECURSIVE forest AS (
			SELECT id, id AS root_id, 0 AS depth FROM tasks
			WHERE id IN (`+ph+`) AND parent_task_id IS NULL
			UNION ALL
			SELECT t.id, f.root_id, f.depth + 1 FROM tasks t JOIN forest f ON t.parent_task_id = f.id
			WHERE f.depth < ?
		)
		SELECT f.id, f.root_id, f.depth, t.user_id,
			COALESCE(t.handler_name, '') AS handler_name, COALESCE(t.handler_type, '') AS handler_type,
			t.start_time
		FROM forest f JOIN tasks t ON t.id = f.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.SessionForest: %w", err)
	}
	out := make([]ForestNode, 0, len(rows))
	for _, n := range rows {
		out = append(out, *n)
	}
	return out, nil
}

func (r *TaskRepo) GetAllDescendantIDs(ctx context.Context, taskID string) ([]string, error) {
	ids, _, err := r.DescendantIDs(ctx, taskID)
	return ids, err
}

type TaskHandler struct {
	HandlerName string `db:"handler_name"`
	HandlerType string `db:"handler_type"`
}

func (r *TaskRepo) HandlersForTasks(ctx context.Context, taskIDs []string) ([]TaskHandler, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.HandlersForTasks: %w", err)
	}
	query := fmt.Sprintf(`SELECT DISTINCT COALESCE(handler_name, '') AS handler_name, COALESCE(handler_type, '') AS handler_type
		FROM tasks WHERE id IN (%s)`, ph)
	rows, err := dbutil.QueryRowPtrs[TaskHandler](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.HandlersForTasks: %w", err)
	}
	out := make([]TaskHandler, 0, len(rows))
	for _, h := range rows {
		out = append(out, *h)
	}
	return out, nil
}

func (r *TaskRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	beforeMs := before.UnixMilli()
	var total int64
	query := `DELETE FROM tasks WHERE id IN (
				SELECT id FROM tasks WHERE start_time < ? LIMIT ?
			)`
	for {
		result, err := r.db.ExecContext(ctx, query, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("TaskRepo.DeleteOlderThan: %w", err)
		}
		n, _ := result.RowsAffected()
		total += n
		if n < int64(batchSize) {
			break
		}
	}
	return total, nil
}

func (r *TaskRepo) FindStaleTasks(ctx context.Context, defaultTimeoutMs int64, limit int) ([]*Task, error) {
	nowMs := time.Now().UnixMilli()
	return dbutil.QueryRowPtrs[Task](ctx, r.db,
		`SELECT id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
			token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
			max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens, agent_name, error_message, handler_name, handler_type
		 FROM tasks
		 WHERE end_time IS NULL
		   AND (status IS NULL OR status NOT IN ('timeout', 'error'))
		   AND last_activity_time + COALESCE(max_execution_time_ms, ?) < ?
		 ORDER BY last_activity_time ASC
		 LIMIT ?`,
		defaultTimeoutMs, nowMs, limit)
}

func (r *TaskRepo) TimeoutTask(ctx context.Context, id string) (bool, error) {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'timeout', end_time = ?, last_activity_time = ? WHERE id = ? AND end_time IS NULL`,
		now, now, id,
	)
	if err != nil {
		return false, fmt.Errorf("TaskRepo.TimeoutTask(%s): %w", id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("TaskRepo.TimeoutTask(%s) rows affected: %w", id, err)
	}
	return n > 0, nil
}

func (r *TaskRepo) TimeoutTasks(ctx context.Context, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders, idArgs, err := dbutil.InClause(ids)
	if err != nil {
		return 0, fmt.Errorf("TaskRepo.TimeoutTasks: %w", err)
	}
	now := time.Now().UnixMilli()
	args := make([]any, 0, len(idArgs)+2)
	args = append(args, now, now)
	args = append(args, idArgs...)
	result, err := r.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'timeout', end_time = ?, last_activity_time = ?
		 WHERE id IN (`+placeholders+`) AND end_time IS NULL`,
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("TaskRepo.TimeoutTasks: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("TaskRepo.TimeoutTasks rows affected: %w", err)
	}
	return n, nil
}

func taskKindOrDefault(kind string) string {
	if kind == "" {
		return TaskKindNormal
	}
	return kind
}

func taskSearchCondition(term string) (string, []any) {
	ownerArgs := []any{term, strings.ToLower(term)}
	id, err := uuid.Parse(term)
	if err != nil {
		return "user_id IN (?, ?)", ownerArgs
	}
	canonical := id.String()
	return "(id = ? OR session_id = ? OR user_id IN (?, ?))", append([]any{canonical, canonical}, ownerArgs...)
}

func canonicalUUIDs(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if id, err := uuid.Parse(v); err == nil {
			out = append(out, id.String())
		}
	}
	return out
}
