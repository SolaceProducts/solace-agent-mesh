// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type Notification struct {
	ID        string `db:"id"`
	UserID    string `db:"user_id"`
	Title     string `db:"title"`
	Body      string `db:"body"`
	Payload   string `db:"payload"`
	Source    string `db:"source"`
	Category  string `db:"category"`
	Status    string `db:"status"`
	CreatedAt int64  `db:"created_at"`
	ReadAt    *int64 `db:"read_at"`
}

type NotificationRepo struct {
	db *DB
}

func NewNotificationRepo(db *DB) *NotificationRepo { return &NotificationRepo{db: db} }

type NewNotification struct {
	UserID   string
	Title    string
	Body     string
	Metadata map[string]any
	Source   string
	Category string
}

func (r *NotificationRepo) Add(ctx context.Context, userID, title, body string, metadata map[string]any, source, category string) (string, error) {
	return addOn(ctx, r.db, NewNotification{UserID: userID, Title: title, Body: body, Metadata: metadata, Source: source, Category: category})
}

func (r *NotificationRepo) AddDeduped(ctx context.Context, n NewNotification, dedup string) (id string, inserted bool, err error) {
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		claimed, cerr := claimDedupOn(ctx, tx, n.UserID, dedup)
		if cerr != nil || !claimed {
			return cerr
		}
		id, cerr = addOn(ctx, tx, n)
		inserted = cerr == nil
		return cerr
	})
	if err != nil {
		return "", false, err
	}
	return id, inserted, nil
}

func (r *NotificationRepo) ClaimDedup(ctx context.Context, userID, dedup string) (claimed bool, err error) {
	return claimDedupOn(ctx, r.db, userID, dedup)
}

func (r *NotificationRepo) HasDedup(ctx context.Context, userID, dedup string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notify_dedup_keys WHERE user_id = ? AND dedup = ?`, userID, dedup,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("read dedup key: %w", err)
	}
	return n > 0, nil
}

func (r *NotificationRepo) DeleteDedupKeysOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM notify_dedup_keys WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete old dedup keys: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

func claimDedupOn(ctx context.Context, q dbutil.Querier, userID, dedup string) (bool, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO notify_dedup_keys (user_id, dedup, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (user_id, dedup) DO NOTHING`,
		userID, dedup, nowMillis(),
	)
	if err != nil {
		return false, fmt.Errorf("claim dedup key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim dedup key rows affected: %w", err)
	}
	return n > 0, nil
}

func addOn(ctx context.Context, q dbutil.Querier, n NewNotification) (string, error) {
	id := idgen.New()
	payload := ""
	if len(n.Metadata) > 0 {
		b, err := json.Marshal(n.Metadata)
		if err != nil {
			return "", fmt.Errorf("marshal notification payload: %w", err)
		}
		payload = string(b)
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO notifications (id, user_id, title, body, payload, source, category, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'unread', ?)`,
		id, n.UserID, n.Title, n.Body, payload, n.Source, n.Category, nowMillis(),
	)
	if err != nil {
		return "", fmt.Errorf("insert notification: %w", err)
	}
	return id, nil
}

type InboxFilter struct {
	UnreadOnly bool
	Category   string
}

func (f InboxFilter) conditions() (string, []any) {
	var sql string
	var args []any
	if f.UnreadOnly {
		sql += ` AND status = 'unread'`
	}
	if f.Category != "" {
		sql += ` AND category = ?`
		args = append(args, f.Category)
	}
	return sql, args
}

func (r *NotificationRepo) ListByUser(ctx context.Context, userID string, filter InboxFilter, limit, offset int) ([]Notification, error) {
	where, filterArgs := filter.conditions()
	query := `SELECT id, user_id, title, body, payload, source, category, status, created_at, read_at
	          FROM notifications WHERE user_id = ?` + where + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args := append([]any{userID}, filterArgs...)
	args = append(args, limit, offset)
	return dbutil.QueryRows[Notification](ctx, r.db, query, args...)
}

func (r *NotificationRepo) CountByUser(ctx context.Context, userID string, filter InboxFilter) (int, error) {
	where, filterArgs := filter.conditions()
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = ?` + where
	var n int
	if err := r.db.QueryRowContext(ctx, query, append([]any{userID}, filterArgs...)...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count notifications: %w", err)
	}
	return n, nil
}

func (r *NotificationRepo) MarkRead(ctx context.Context, userID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET status = 'read', read_at = ?
		 WHERE id = ? AND user_id = ? AND status = 'unread'`,
		nowMillis(), id, userID,
	)
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		exists, eerr := r.exists(ctx, userID, id)
		if eerr != nil {
			return eerr
		}
		if !exists {
			return sql.ErrNoRows
		}
	}
	return nil
}

func (r *NotificationRepo) MarkUnread(ctx context.Context, userID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET status = 'unread', read_at = NULL
		 WHERE id = ? AND user_id = ? AND status = 'read'`,
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("mark notification unread: %w", err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		exists, eerr := r.exists(ctx, userID, id)
		if eerr != nil {
			return eerr
		}
		if !exists {
			return sql.ErrNoRows
		}
	}
	return nil
}

func (r *NotificationRepo) MarkAllRead(ctx context.Context, userID string, filter InboxFilter) (int64, error) {
	where, filterArgs := filter.conditions()
	args := append([]any{nowMillis(), userID}, filterArgs...)
	res, err := r.db.ExecContext(ctx,
		`UPDATE notifications SET status = 'read', read_at = ?
		 WHERE user_id = ? AND status = 'unread'`+where,
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (r *NotificationRepo) DeleteOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE created_at < ? AND status = 'read'`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete old notifications: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

func (r *NotificationRepo) Delete(ctx context.Context, userID, id string) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete notification: %w", err)
	}
	if affected, aerr := res.RowsAffected(); aerr == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *NotificationRepo) DeleteAllByUser(ctx context.Context, userID string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete all notifications: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (r *NotificationRepo) Get(ctx context.Context, userID, id string) (*Notification, error) {
	n, err := dbutil.QueryRow[Notification](ctx, r.db,
		`SELECT id, user_id, title, body, payload, source, category, status, created_at, read_at
		 FROM notifications WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return nil, fmt.Errorf("get notification: %w", err)
	}
	if n == nil {
		return nil, sql.ErrNoRows
	}
	return n, nil
}

func (r *NotificationRepo) exists(ctx context.Context, userID, id string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM notifications WHERE id = ? AND user_id = ?`, id, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check notification exists: %w", err)
	}
	return true, nil
}

func nowMillis() int64 { return time.Now().UnixMilli() }
