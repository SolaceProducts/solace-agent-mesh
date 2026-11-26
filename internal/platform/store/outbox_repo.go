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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type OutboxRepo struct {
	db *DB
}

func NewOutboxRepo(db *DB) *OutboxRepo {
	return &OutboxRepo{db: db}
}

func (r *OutboxRepo) Create(ctx context.Context, e *OutboxEvent) error {
	return r.createOn(ctx, r.db, e)
}

func (r *OutboxRepo) CreateInTx(ctx context.Context, tx *dbutil.Tx, e *OutboxEvent) error {
	return r.createOn(ctx, tx, e)
}

func (r *OutboxRepo) createOn(ctx context.Context, q dbutil.Querier, e *OutboxEvent) error {
	if e.ID == "" {
		e.ID = idgen.New()
	}
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().UnixMilli()
	}

	published := 0
	if e.Published {
		published = 1
	}

	_, err := q.ExecContext(ctx,
		`INSERT INTO outbox_events (id, event_type, aggregate_id, payload, created_at, published, published_at, next_attempt_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.EventType, e.AggregateID, e.Payload, e.CreatedAt, published, e.PublishedAt, e.NextAttemptAt,
	)
	if err != nil {
		return fmt.Errorf("OutboxRepo.Create(%s): %w", e.ID, err)
	}
	return nil
}

const outboxEventCols = `id, event_type, aggregate_id, payload, created_at,
	published, published_at, retry_count, last_error, next_attempt_at, dead`

func (r *OutboxRepo) GetDispatchable(ctx context.Context, limit int) ([]OutboxEvent, error) {
	now := time.Now().UnixMilli()
	return dbutil.QueryRows[OutboxEvent](ctx, r.db,
		`SELECT `+outboxEventCols+`
		 FROM outbox_events
		 WHERE published = 0 AND dead = 0 AND next_attempt_at <= ?
		 ORDER BY created_at ASC LIMIT ?`,
		now, limit,
	)
}

func (r *OutboxRepo) HasDispatchable(ctx context.Context) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM outbox_events
		 WHERE published = 0 AND dead = 0 AND next_attempt_at <= ?
		 LIMIT 1`,
		time.Now().UnixMilli(),
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *OutboxRepo) Dialect() string {
	return r.db.Dialect()
}

func (r *OutboxRepo) WithTx(ctx context.Context, fn func(tx *dbutil.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

func (r *OutboxRepo) ClaimDispatchable(ctx context.Context, tx *dbutil.Tx, limit int) ([]OutboxEvent, error) {
	now := time.Now().UnixMilli()
	query := `SELECT ` + outboxEventCols + `
		 FROM outbox_events
		 WHERE published = 0 AND dead = 0 AND next_attempt_at <= ?
		 ORDER BY created_at ASC LIMIT ?`
	if tx.Dialect() == "postgres" {
		query += ` FOR UPDATE SKIP LOCKED`
	}
	return dbutil.QueryRows[OutboxEvent](ctx, tx, query, now, limit)
}

func (r *OutboxRepo) MarkPublished(ctx context.Context, ids []string) error {
	return markPublishedOn(ctx, r.db, ids)
}

func (r *OutboxRepo) MarkPublishedTx(ctx context.Context, tx *dbutil.Tx, ids []string) error {
	return markPublishedOn(ctx, tx, ids)
}

func markPublishedOn(ctx context.Context, q dbutil.Querier, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	now := time.Now().UnixMilli()
	ph, idArgs, err := dbutil.InClause(ids)
	if err != nil {
		return fmt.Errorf("OutboxRepo.MarkPublished: %w", err)
	}
	args := append([]any{now}, idArgs...)

	_, err = q.ExecContext(ctx,
		fmt.Sprintf(`UPDATE outbox_events SET published = 1, published_at = ? WHERE id IN (%s)`, ph),
		args...,
	)
	if err != nil {
		return fmt.Errorf("OutboxRepo.MarkPublished: %w", err)
	}
	return nil
}

func (r *OutboxRepo) MarkFailed(ctx context.Context, id, lastError string, retryCount int, nextAttemptAt int64, dead bool) error {
	return markFailedOn(ctx, r.db, id, lastError, retryCount, nextAttemptAt, dead)
}

func (r *OutboxRepo) MarkFailedTx(ctx context.Context, tx *dbutil.Tx, id, lastError string, retryCount int, nextAttemptAt int64, dead bool) error {
	return markFailedOn(ctx, tx, id, lastError, retryCount, nextAttemptAt, dead)
}

func markFailedOn(ctx context.Context, q dbutil.Querier, id, lastError string, retryCount int, nextAttemptAt int64, dead bool) error {
	deadVal := 0
	if dead {
		deadVal = 1
	}
	_, err := q.ExecContext(ctx,
		`UPDATE outbox_events
		 SET retry_count = ?, last_error = ?, next_attempt_at = ?, dead = ?
		 WHERE id = ?`,
		retryCount, lastError, nextAttemptAt, deadVal, id,
	)
	if err != nil {
		return fmt.Errorf("OutboxRepo.MarkFailed(%s): %w", id, err)
	}
	return nil
}

func (r *OutboxRepo) Cleanup(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM outbox_events WHERE published = 1 AND published_at < ?`,
		olderThan.UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("OutboxRepo.Cleanup: %w", err)
	}
	return result.RowsAffected()
}
