// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type SSEEventRepo struct {
	db *DB
}

func NewSSEEventRepo(db *DB) *SSEEventRepo {
	return &SSEEventRepo{db: db}
}

func (r *SSEEventRepo) Append(ctx context.Context, row *SSEEventBufferRow) error {
	row.CreatedAt = time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at, broker_message_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (task_id, event_sequence) DO NOTHING`,
		row.TaskID, row.SessionID, row.UserID, row.EventSequence,
		row.EventType, row.EventData, row.CreatedAt, nullableBrokerMessageID(row.BrokerMessageID),
	)
	if err != nil {
		return fmt.Errorf("SSEEventRepo.Append(%s): %w", row.TaskID, err)
	}
	return nil
}

func (r *SSEEventRepo) AppendBatch(ctx context.Context, rows []*SSEEventBufferRow) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	var sb strings.Builder
	sb.WriteString("INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at, broker_message_id) VALUES ")
	args := make([]any, 0, len(rows)*8)
	for i, row := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?, ?, ?, ?, ?, ?, ?)")
		args = append(args, row.TaskID, row.SessionID, row.UserID, row.EventSequence,
			row.EventType, row.EventData, row.CreatedAt, nullableBrokerMessageID(row.BrokerMessageID))
	}
	sb.WriteString(" ON CONFLICT (task_id, event_sequence) DO NOTHING")
	res, err := r.db.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.AppendBatch(%d): %w", len(rows), err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return int64(len(rows)), nil
	}
	return inserted, nil
}

const sseEventSelect = `SELECT s.task_id, s.session_id, s.user_id, s.event_sequence, s.event_type, s.event_data, s.created_at,
		CASE WHEN c.consumed_through_seq >= s.event_sequence THEN 1 ELSE 0 END AS consumed,
		CASE WHEN c.consumed_through_seq >= s.event_sequence THEN c.consumed_at END AS consumed_at
	FROM sse_event_buffer s
	LEFT JOIN sse_task_consumed c ON c.task_id = s.task_id`

func (r *SSEEventRepo) GetByTask(ctx context.Context, taskID string, fromSeq int) ([]*SSEEventBufferRow, error) {
	return dbutil.QueryRowPtrs[SSEEventBufferRow](ctx, r.db,
		sseEventSelect+`
		 WHERE s.task_id = ? AND s.event_sequence >= ?
		 ORDER BY s.event_sequence`,
		taskID, fromSeq)
}

func (r *SSEEventRepo) Owner(ctx context.Context, taskID string) (string, error) {
	var owner string
	err := r.db.QueryRowContext(ctx,
		`SELECT user_id FROM sse_event_buffer WHERE task_id = ? AND user_id <> '' LIMIT 1`,
		taskID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("SSEEventRepo.Owner(%s): %w", taskID, err)
	}
	return owner, nil
}

type TaskLogState struct {
	MaxSeq    int
	Completed bool
}

func (r *SSEEventRepo) SequenceStates(ctx context.Context, taskIDs []string) (map[string]TaskLogState, error) {
	out := make(map[string]TaskLogState, len(taskIDs))
	for start := 0; start < len(taskIDs); start += dbutil.InClauseCap {
		if err := r.sequenceStates(ctx, taskIDs[start:min(start+dbutil.InClauseCap, len(taskIDs))], out); err != nil {
			return nil, fmt.Errorf("SSEEventRepo.SequenceStates: %w", err)
		}
	}
	return out, nil
}

func (r *SSEEventRepo) sequenceStates(ctx context.Context, taskIDs []string, out map[string]TaskLogState) error {
	_, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return err
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT s.task_id, s.event_sequence, s.completed FROM %s
		 JOIN sse_event_buffer s ON s.task_id = v.id AND s.event_sequence =
		     (SELECT MAX(m.event_sequence) FROM sse_event_buffer m WHERE m.task_id = v.id)`,
		taskIDRows(r.db.Dialect(), len(taskIDs))), args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var taskID string
		var maxSeq, done int
		if err := rows.Scan(&taskID, &maxSeq, &done); err != nil {
			return err
		}
		out[taskID] = TaskLogState{MaxSeq: maxSeq, Completed: done != 0}
	}
	return rows.Err()
}

func taskIDRows(dialect string, n int) string {
	if dialect == "postgres" {
		return "unnest(CAST(ARRAY[" + strings.Repeat("?,", n-1) + "?] AS uuid[])) AS v(id)"
	}
	return "(SELECT column1 AS id FROM (VALUES " + strings.Repeat("(?),", n-1) + "(?))) AS v"
}

func (r *SSEEventRepo) GetMaxSequence(ctx context.Context, taskID string) (int, error) {
	maxSeq, err := dbutil.QueryVal[sql.NullInt64](ctx, r.db,
		`SELECT MAX(event_sequence) FROM sse_event_buffer WHERE task_id = ?`, taskID)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.GetMaxSequence(%s): %w", taskID, err)
	}
	if !maxSeq.Valid {
		return 0, nil
	}
	return int(maxSeq.Int64), nil
}

func (r *SSEEventRepo) SequenceState(ctx context.Context, taskID string) (maxSeq int, completed bool, err error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(event_sequence), 0), COALESCE(MAX(completed), 0)
		 FROM sse_event_buffer WHERE task_id = ?`, taskID)
	var done int
	if err := row.Scan(&maxSeq, &done); err != nil {
		return 0, false, fmt.Errorf("SSEEventRepo.SequenceState(%s): %w", taskID, err)
	}
	return maxSeq, done != 0, nil
}

func (r *SSEEventRepo) BrokerMessageSequence(ctx context.Context, taskID, brokerMessageID string) (int, error) {
	seq, err := dbutil.QueryVal[sql.NullInt64](ctx, r.db,
		`SELECT MIN(event_sequence) FROM sse_event_buffer WHERE task_id = ? AND broker_message_id = ?`, taskID, brokerMessageID)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.BrokerMessageSequence(%s): %w", taskID, err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return int(seq.Int64), nil
}

func nullableBrokerMessageID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (r *SSEEventRepo) MarkConsumed(ctx context.Context, taskID string) error {
	return r.markConsumed(ctx, taskID, "MarkConsumed", "task_id = ?", taskID)
}

func (r *SSEEventRepo) MarkConsumedInSession(ctx context.Context, taskID, sessionID string) error {
	return r.markConsumed(ctx, taskID, "MarkConsumedInSession", "task_id = ? AND session_id = ?", taskID, sessionID)
}

func (r *SSEEventRepo) markConsumed(ctx context.Context, taskID, op, where string, args ...any) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sse_task_consumed (task_id, consumed_through_seq, consumed_at)
		 SELECT task_id, MAX(event_sequence), CAST(? AS BIGINT)
		 FROM sse_event_buffer WHERE `+where+` GROUP BY task_id
		 ON CONFLICT (task_id) DO UPDATE SET
		     consumed_through_seq = EXCLUDED.consumed_through_seq, consumed_at = EXCLUDED.consumed_at
		 WHERE EXCLUDED.consumed_through_seq > sse_task_consumed.consumed_through_seq`,
		append([]any{now}, args...)...,
	)
	if err != nil {
		return fmt.Errorf("SSEEventRepo.%s(%s): %w", op, taskID, err)
	}
	return nil
}

func (r *SSEEventRepo) MarkCompleted(ctx context.Context, taskID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET completed = 1
		 WHERE task_id = ? AND event_sequence = (
		     SELECT MAX(event_sequence) FROM sse_event_buffer WHERE task_id = ?
		 )`,
		taskID, taskID,
	)
	if err != nil {
		return fmt.Errorf("SSEEventRepo.MarkCompleted(%s): %w", taskID, err)
	}
	return nil
}

func (r *SSEEventRepo) MarkCompletedBatch(ctx context.Context, taskIDs []string) error {
	for start := 0; start < len(taskIDs); start += dbutil.InClauseCap {
		in, args, err := dbutil.InClause(taskIDs[start:min(start+dbutil.InClauseCap, len(taskIDs))])
		if err != nil {
			return fmt.Errorf("SSEEventRepo.MarkCompletedBatch: %w", err)
		}
		if _, err := r.db.ExecContext(ctx,
			`UPDATE sse_event_buffer SET completed = 1
			 WHERE task_id IN (`+in+`) AND event_sequence = (
			     SELECT MAX(m.event_sequence) FROM sse_event_buffer m WHERE m.task_id = sse_event_buffer.task_id
			 )`, args...); err != nil {
			return fmt.Errorf("SSEEventRepo.MarkCompletedBatch(%d tasks): %w", len(taskIDs), err)
		}
	}
	return nil
}

func (r *SSEEventRepo) IsCompleted(ctx context.Context, taskID string) (bool, error) {
	n, err := dbutil.QueryVal[int64](ctx, r.db,
		`SELECT COUNT(*) FROM sse_event_buffer WHERE task_id = ? AND completed = 1`, taskID)
	if err != nil {
		return false, fmt.Errorf("SSEEventRepo.IsCompleted(%s): %w", taskID, err)
	}
	return n > 0, nil
}

func (r *SSEEventRepo) DeleteByTask(ctx context.Context, taskID string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM sse_event_buffer WHERE task_id = ?`, taskID,
		); err != nil {
			return fmt.Errorf("SSEEventRepo.DeleteByTask(%s): %w", taskID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sse_task_consumed WHERE task_id = ?`, taskID); err != nil {
			return fmt.Errorf("SSEEventRepo.DeleteByTask(%s) consumed mark: %w", taskID, err)
		}
		return nil
	})
}

func (r *SSEEventRepo) FindSequenceAfterTimestamp(ctx context.Context, taskID string, epochMs int64) (int, error) {
	seq, err := dbutil.QueryVal[sql.NullInt64](ctx, r.db,
		`SELECT MIN(event_sequence) FROM sse_event_buffer WHERE task_id = ? AND created_at > ?`,
		taskID, epochMs)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.FindSequenceAfterTimestamp(%s): %w", taskID, err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return int(seq.Int64), nil
}

func (r *SSEEventRepo) DeleteOlderThan(ctx context.Context, before time.Time, _ int) (int64, error) {
	beforeMs := before.UnixMilli()
	taskIDs, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT DISTINCT o.task_id FROM sse_event_buffer o
		 WHERE o.created_at < ?
		   AND NOT EXISTS (
		       SELECT 1 FROM sse_event_buffer n
		       WHERE n.task_id = o.task_id AND n.created_at >= ?)`,
		beforeMs, beforeMs)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.DeleteOlderThan select: %w", err)
	}
	total, err := r.deleteByTaskIDs(ctx, taskIDs, "DeleteOlderThan")
	if err != nil {
		return total, err
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sse_task_consumed WHERE NOT EXISTS (
			SELECT 1 FROM sse_event_buffer s WHERE s.task_id = sse_task_consumed.task_id)`); err != nil {
		return total, fmt.Errorf("SSEEventRepo.DeleteOlderThan consumed marks: %w", err)
	}
	return total, nil
}

const sseTaskDeleteChunk = 500

func (r *SSEEventRepo) DeleteConsumedTasksBefore(ctx context.Context, cutoffEpochMs int64) (int64, error) {
	taskIDs, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT c.task_id
		 FROM sse_task_consumed c
		 JOIN tasks t ON t.id = c.task_id
		 WHERE t.end_time IS NOT NULL AND c.consumed_at < ?
		   AND (SELECT MAX(s.event_sequence) FROM sse_event_buffer s WHERE s.task_id = c.task_id) <= c.consumed_through_seq`,
		cutoffEpochMs)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.DeleteConsumedTasksBefore select: %w", err)
	}
	return r.deleteByTaskIDs(ctx, taskIDs, "DeleteConsumedTasksBefore")
}

func (r *SSEEventRepo) DeleteForTasksEndedBefore(ctx context.Context, cutoffEpochMs int64) (int64, error) {
	taskIDs, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT DISTINCT s.task_id
		 FROM sse_event_buffer s
		 JOIN tasks t ON t.id = s.task_id
		 WHERE t.end_time IS NOT NULL AND t.end_time < ?`,
		cutoffEpochMs)
	if err != nil {
		return 0, fmt.Errorf("SSEEventRepo.DeleteForTasksEndedBefore select: %w", err)
	}
	return r.deleteByTaskIDs(ctx, taskIDs, "DeleteForTasksEndedBefore")
}

func (r *SSEEventRepo) deleteByTaskIDs(ctx context.Context, taskIDs []string, caller string) (int64, error) {
	var total int64
	for start := 0; start < len(taskIDs); start += sseTaskDeleteChunk {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		end := min(start+sseTaskDeleteChunk, len(taskIDs))
		ph, args, err := dbutil.InClause(taskIDs[start:end])
		if err != nil {
			return total, fmt.Errorf("SSEEventRepo.%s inclause: %w", caller, err)
		}
		var n int64
		err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
			result, execErr := tx.ExecContext(ctx,
				`DELETE FROM sse_event_buffer WHERE task_id IN (`+ph+`)`, args...)
			if execErr != nil {
				return fmt.Errorf("SSEEventRepo.%s delete: %w", caller, execErr)
			}
			n, _ = result.RowsAffected()
			if _, execErr = tx.ExecContext(ctx,
				`DELETE FROM sse_task_consumed WHERE task_id IN (`+ph+`)`, args...); execErr != nil {
				return fmt.Errorf("SSEEventRepo.%s delete consumed marks: %w", caller, execErr)
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (r *SSEEventRepo) GetUnconsumedBySession(ctx context.Context, sessionID string) (map[string][]*SSEEventBufferRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.task_id, s.session_id, s.user_id, s.event_sequence, s.event_type, s.event_data, s.created_at,
		        0 AS consumed, CAST(NULL AS BIGINT) AS consumed_at
		 FROM sse_event_buffer s
		 WHERE s.session_id = ?
		   AND NOT EXISTS (SELECT 1 FROM sse_task_consumed c
		                   WHERE c.task_id = s.task_id AND c.consumed_through_seq >= s.event_sequence)
		 ORDER BY s.event_sequence`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("SSEEventRepo.GetUnconsumedBySession(%s): %w", sessionID, err)
	}
	items, err := dbutil.ScanRowPtrs[SSEEventBufferRow](rows)
	if err != nil {
		return nil, fmt.Errorf("SSEEventRepo.GetUnconsumedBySession(%s) scan: %w", sessionID, err)
	}
	result := make(map[string][]*SSEEventBufferRow)
	for _, row := range items {
		result[row.TaskID] = append(result[row.TaskID], row)
	}
	return result, nil
}

func (r *SSEEventRepo) FinalResponse(ctx context.Context, taskID string) (data string, found bool, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT event_data FROM sse_event_buffer WHERE task_id = ? AND event_type = 'final_response' ORDER BY event_sequence DESC LIMIT 1`,
		taskID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("SSEEventRepo.FinalResponse(%s): %w", taskID, err)
	}
	return data, true, nil
}
