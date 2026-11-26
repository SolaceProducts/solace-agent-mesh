// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const runningTaskUpsertBatch = 100

func (s *SQLStore) RegisterRunningTask(ctx context.Context, task RunningTask, ownerID string, until time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session_running_tasks (task_id, app_name, user_id, session_id, owner_id, lease_expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (task_id) DO UPDATE SET
		   app_name = excluded.app_name, user_id = excluded.user_id, session_id = excluded.session_id,
		   owner_id = excluded.owner_id, lease_expires_at = excluded.lease_expires_at`,
		task.TaskID, task.AppName, task.UserID, task.SessionID, ownerID, formatDBTime(until))
	return err
}

func (s *SQLStore) RenewRunningTasks(ctx context.Context, ownerID string, tasks []RunningTask, until time.Time) (int, error) {
	if ownerID == "" || len(tasks) == 0 {
		return 0, nil
	}
	lease := formatDBTime(until)
	renewed := 0
	for chunk := range slices.Chunk(tasks, runningTaskUpsertBatch) {
		rows := make([]string, 0, len(chunk))
		args := make([]any, 0, len(chunk)*6)
		for _, t := range chunk {
			rows = append(rows, "(?, ?, ?, ?, ?, ?)")
			args = append(args, t.TaskID, t.AppName, t.UserID, t.SessionID, ownerID, lease)
		}
		result, err := s.db.ExecContext(ctx,
			`INSERT INTO session_running_tasks (task_id, app_name, user_id, session_id, owner_id, lease_expires_at)
			 VALUES `+strings.Join(rows, ", ")+`
			 ON CONFLICT (task_id) DO UPDATE SET lease_expires_at = excluded.lease_expires_at
			 WHERE session_running_tasks.owner_id = excluded.owner_id`,
			args...)
		if err != nil {
			return renewed, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return renewed, err
		}
		renewed += int(affected)
	}
	return renewed, nil
}

func (s *SQLStore) RemoveRunningTask(ctx context.Context, taskID, ownerID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM session_running_tasks WHERE task_id = ? AND owner_id = ?`, taskID, ownerID)
	return err
}

func (s *SQLStore) ListRunningTasks(ctx context.Context, appName, userID, sessionID string, liveAfter time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT task_id FROM session_running_tasks
		  WHERE app_name = ? AND user_id = ? AND session_id = ? AND lease_expires_at > ?`,
		appName, userID, sessionID, formatDBTime(liveAfter))
	if err != nil {
		return nil, err
	}
	return appendScannedTaskIDs(nil, rows)
}

func (s *SQLStore) DeleteLapsedRunningTasks(ctx context.Context, appName string, lapsedBefore time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM session_running_tasks WHERE app_name = ? AND lease_expires_at < ?`,
		appName, formatDBTime(lapsedBefore))
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	return int(affected), err
}

func (s *SQLStore) CheckpointedTasks(ctx context.Context, sessionID string, taskIDs []string) ([]string, error) {
	var found []string
	for chunk := range slices.Chunk(taskIDs, dbutil.InClauseCap) {
		clause, inArgs, err := dbutil.InClause(chunk)
		if err != nil {
			return found, err
		}
		args := append([]any{sessionID}, inArgs...)
		rows, err := s.db.QueryContext(ctx,
			`SELECT task_id FROM task_checkpoints WHERE session_id = ? AND task_id IN (`+clause+`)`, args...)
		if err != nil {
			return found, err
		}
		found, err = appendScannedTaskIDs(found, rows)
		if err != nil {
			return found, err
		}
	}
	return found, nil
}

func appendScannedTaskIDs(found []string, rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return found, err
		}
		found = append(found, id)
	}
	return found, rows.Err()
}

func (s *SQLStore) deleteSessionRunningTasks(ctx context.Context, appName, userID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM session_running_tasks WHERE app_name = ? AND user_id = ? AND session_id = ?`,
		appName, userID, sessionID)
	return err
}
