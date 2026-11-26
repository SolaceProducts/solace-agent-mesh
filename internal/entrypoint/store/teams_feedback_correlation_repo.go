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

type TeamsFeedbackCorrelationRepo struct {
	db *DB
}

func NewTeamsFeedbackCorrelationRepo(db *DB) *TeamsFeedbackCorrelationRepo {
	return &TeamsFeedbackCorrelationRepo{db: db}
}

type TeamsFeedbackCorrelation struct {
	TaskID    string `db:"task_id"`
	SessionID string `db:"session_id"`
	CreatedAt int64  `db:"created_at"`
}

func (r *TeamsFeedbackCorrelationRepo) Put(ctx context.Context, conversationID, messageID, taskID, sessionID string) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO teams_feedback_correlation
		     (conversation_id, message_id, task_id, session_id, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (conversation_id, message_id) DO UPDATE SET
		     task_id = excluded.task_id,
		     session_id = excluded.session_id,
		     created_at = excluded.created_at`,
		conversationID, messageID, taskID, sessionID, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("TeamsFeedbackCorrelationRepo.Put(%s): %w", messageID, err)
	}
	return nil
}

func (r *TeamsFeedbackCorrelationRepo) Get(ctx context.Context, conversationID, messageID string, maxAge time.Duration) (TeamsFeedbackCorrelation, bool, error) {
	row, err := dbutil.QueryRow[TeamsFeedbackCorrelation](ctx, r.db,
		`SELECT task_id, session_id, created_at FROM teams_feedback_correlation
		 WHERE conversation_id = ? AND message_id = ?`, conversationID, messageID)
	if err != nil {
		return TeamsFeedbackCorrelation{}, false, fmt.Errorf("TeamsFeedbackCorrelationRepo.Get(%s): %w", messageID, err)
	}
	if row == nil {
		return TeamsFeedbackCorrelation{}, false, nil
	}
	if time.Since(time.UnixMilli(row.CreatedAt)) > maxAge {
		return TeamsFeedbackCorrelation{}, false, nil
	}
	return *row, true, nil
}

func (r *TeamsFeedbackCorrelationRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	const query = `DELETE FROM teams_feedback_correlation
		  WHERE (conversation_id, message_id) IN (
		    SELECT conversation_id, message_id FROM teams_feedback_correlation
		    WHERE created_at < ? LIMIT ?
		  )`
	beforeMs := before.UnixMilli()
	var total int64
	for {
		result, err := r.db.ExecContext(ctx, query, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("TeamsFeedbackCorrelationRepo.DeleteOlderThan: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("TeamsFeedbackCorrelationRepo.DeleteOlderThan: rows affected: %w", err)
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}
