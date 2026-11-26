// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type HILCard struct {
	RequestID   string `db:"request_id"`
	ChannelType string `db:"channel_type"`
	Card        string `db:"card"`
	CreatedAt   int64  `db:"created_at"`
}

type resolvedCardRequest struct {
	RequestID string `db:"request_id"`
}

type HILCardRepo struct {
	db *DB
}

func NewHILCardRepo(db *DB) *HILCardRepo { return &HILCardRepo{db: db} }

func (r *HILCardRepo) Add(ctx context.Context, requestID, channelType, card string) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO notify_hil_cards (request_id, channel_type, card, created_at) VALUES (?, ?, ?, ?)`,
		requestID, channelType, card, nowMillis()); err != nil {
		return fmt.Errorf("add hil card: %w", err)
	}
	return nil
}

func (r *HILCardRepo) List(ctx context.Context, requestID string) ([]HILCard, error) {
	cards, err := dbutil.QueryRows[HILCard](ctx, r.db,
		`SELECT request_id, channel_type, card, created_at FROM notify_hil_cards WHERE request_id = ? ORDER BY created_at`, requestID)
	if err != nil {
		return nil, fmt.Errorf("list hil cards: %w", err)
	}
	return cards, nil
}

func (r *HILCardRepo) ResolvedRequestIDs(ctx context.Context, resolvedBefore int64, limit int) ([]string, error) {
	rows, err := dbutil.QueryRows[resolvedCardRequest](ctx, r.db,
		`SELECT DISTINCT c.request_id AS request_id
		 FROM notify_hil_cards c JOIN notify_pending_requests p ON p.request_id = c.request_id
		 WHERE p.status != 'pending' AND p.resolved_at IS NOT NULL AND p.resolved_at < ?
		 LIMIT ?`, resolvedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list resolved hil card requests: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.RequestID)
	}
	return ids, nil
}

func (r *HILCardRepo) DeleteListed(ctx context.Context, cards []HILCard) error {
	for _, c := range cards {
		if _, err := r.db.ExecContext(ctx,
			`DELETE FROM notify_hil_cards WHERE request_id = ? AND channel_type = ? AND card = ? AND created_at = ?`,
			c.RequestID, c.ChannelType, c.Card, c.CreatedAt); err != nil {
			return fmt.Errorf("delete listed hil cards: %w", err)
		}
	}
	return nil
}

func (r *HILCardRepo) DeleteOrphans(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notify_hil_cards WHERE request_id NOT IN (SELECT request_id FROM notify_pending_requests)`)
	if err != nil {
		return 0, fmt.Errorf("delete orphan hil cards: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
