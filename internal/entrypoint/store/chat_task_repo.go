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

type ChatTaskRepo struct {
	db *DB
}

func NewChatTaskRepo(db *DB) *ChatTaskRepo {
	return &ChatTaskRepo{db: db}
}

func (r *ChatTaskRepo) Create(ctx context.Context, ct *ChatTask) error {
	now := time.Now().UnixMilli()
	ct.CreatedTime = now
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO chat_tasks (id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, kind)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ct.ID, ct.SessionID, ct.UserID, ct.UserMessage, ct.MessageBubbles, ct.TaskMetadata, ct.CreatedTime,
			chatTaskKindOrDefault(ct.Kind),
		); err != nil {
			return err
		}
		return markSessionActiveTx(ctx, tx, ct.SessionID)
	})
	if err != nil {
		return fmt.Errorf("ChatTaskRepo.Create(%s): %w", ct.ID, err)
	}
	return nil
}

func chatTaskKindOrDefault(kind string) string {
	if kind == "" {
		return ChatTaskKindNormal
	}
	return kind
}

func (r *ChatTaskRepo) GetByID(ctx context.Context, id string) (*ChatTask, error) {
	return dbutil.QueryRow[ChatTask](ctx, r.db,
		`SELECT id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind
		 FROM chat_tasks WHERE id = ?`, id)
}

func (r *ChatTaskRepo) GetBySessionAndID(ctx context.Context, sessionID, id string) (*ChatTask, error) {
	return dbutil.QueryRow[ChatTask](ctx, r.db,
		`SELECT id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind
		 FROM chat_tasks WHERE session_id = ? AND id = ?`, sessionID, id)
}

func (r *ChatTaskRepo) ListBySession(ctx context.Context, sessionID string) ([]*ChatTask, error) {
	return dbutil.QueryRowPtrs[ChatTask](ctx, r.db,
		`SELECT id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind
		 FROM chat_tasks WHERE session_id = ? ORDER BY created_time ASC`, sessionID)
}

func (r *ChatTaskRepo) ListBySessionPage(ctx context.Context, sessionID string, limit, offset int) ([]*ChatTask, int, error) {
	return listPageWithDerivedTotal[ChatTask](ctx, r.db,
		`id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind`,
		"chat_tasks", "WHERE session_id = ?", "created_time ASC, id ASC",
		[]any{sessionID}, limit, offset, "ChatTaskRepo.ListBySessionPage")
}

func (r *ChatTaskRepo) ListBySessionForUser(ctx context.Context, sessionID, userID string) ([]*ChatTask, error) {
	return dbutil.QueryRowPtrs[ChatTask](ctx, r.db,
		`SELECT id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind
		 FROM chat_tasks WHERE session_id = ? AND user_id = ? ORDER BY created_time ASC`, sessionID, userID)
}

func (r *ChatTaskRepo) ListRecentBySessionForUser(ctx context.Context, sessionID, userID string, limit int) ([]*ChatTask, error) {
	return dbutil.QueryRowPtrs[ChatTask](ctx, r.db,
		`SELECT id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind
		 FROM chat_tasks WHERE session_id = ? AND user_id = ?
		 ORDER BY created_time DESC, id DESC LIMIT ?`, sessionID, userID, limit)
}

func (r *ChatTaskRepo) CountBySessionForUser(ctx context.Context, sessionID, userID string) (int, error) {
	return dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM chat_tasks
		 WHERE session_id = ? AND user_id = ? AND kind = 'task'`,
		sessionID, userID)
}

func (r *ChatTaskRepo) ListBySessionIDs(ctx context.Context, sessionIDs []string) (map[string][]*ChatTask, error) {
	if len(sessionIDs) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(sessionIDs)
	if err != nil {
		return nil, fmt.Errorf("ChatTaskRepo.ListBySessionIDs: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind
		 FROM chat_tasks WHERE session_id IN (%s) ORDER BY created_time ASC`, ph,
	)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ChatTaskRepo.ListBySessionIDs: %w", err)
	}
	items, err := dbutil.ScanRowPtrs[ChatTask](rows)
	if err != nil {
		return nil, fmt.Errorf("ChatTaskRepo.ListBySessionIDs scan: %w", err)
	}
	result := make(map[string][]*ChatTask, len(sessionIDs))
	for _, ct := range items {
		result[ct.SessionID] = append(result[ct.SessionID], ct)
	}
	return result, nil
}

const chatTaskSameOwnerAndSession = `chat_tasks.user_id = EXCLUDED.user_id AND chat_tasks.session_id = EXCLUDED.session_id`

func (r *ChatTaskRepo) Upsert(ctx context.Context, ct *ChatTask) (bool, error) {
	now := time.Now().UnixMilli()
	if ct.CreatedTime == 0 {
		ct.CreatedTime = now
	}
	insertSQL := `INSERT INTO chat_tasks (id, session_id, user_id, user_message, message_bubbles, task_metadata, created_time, updated_time, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertWhereSQL(r.db.Dialect(), insertSQL, "id",
		[]string{"user_message", "message_bubbles", "task_metadata", "updated_time"},
		chatTaskSameOwnerAndSession)
	written := false
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		res, err := tx.ExecContext(ctx, upsertSQL,
			ct.ID, ct.SessionID, ct.UserID, ct.UserMessage, ct.MessageBubbles, ct.TaskMetadata, ct.CreatedTime, now,
			chatTaskKindOrDefault(ct.Kind))
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		written = affected > 0
		if !written {
			return nil
		}
		return markSessionActiveTx(ctx, tx, ct.SessionID)
	})
	if err != nil {
		return false, fmt.Errorf("ChatTaskRepo.Upsert(%s): %w", ct.ID, err)
	}
	return written, nil
}

func (r *ChatTaskRepo) UpdateBubbles(ctx context.Context, id, bubbles string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE chat_tasks SET message_bubbles = ?, updated_time = ? WHERE id = ?`,
		bubbles, now, id,
	)
	if err != nil {
		return fmt.Errorf("ChatTaskRepo.UpdateBubbles(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}
