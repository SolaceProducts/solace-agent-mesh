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
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type EvalWatchlistRepo struct {
	db *DB
}

func NewEvalWatchlistRepo(db *DB) *EvalWatchlistRepo {
	return &EvalWatchlistRepo{db: db}
}

const evalWatchlistSelect = `SELECT id, user_id, agent_name, position, created_time FROM eval_agent_watchlist`

func (r *EvalWatchlistRepo) ListForUser(ctx context.Context, userID string) ([]*EvalAgentWatchlist, error) {
	rows, err := dbutil.QueryRowPtrs[EvalAgentWatchlist](ctx, r.db,
		evalWatchlistSelect+` WHERE user_id = ? ORDER BY position ASC, created_time ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("EvalWatchlistRepo.ListForUser(%s): %w", userID, err)
	}
	return rows, nil
}

func (r *EvalWatchlistRepo) ListPaginatedForUser(ctx context.Context, userID string, pageNumber, pageSize int) ([]*EvalAgentWatchlist, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM eval_agent_watchlist WHERE user_id = ?`, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalWatchlistRepo.ListPaginatedForUser count(%s): %w", userID, err)
	}
	rows, err := dbutil.QueryRowPtrs[EvalAgentWatchlist](ctx, r.db,
		evalWatchlistSelect+` WHERE user_id = ? ORDER BY position ASC, created_time ASC, id ASC LIMIT ? OFFSET ?`,
		userID, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalWatchlistRepo.ListPaginatedForUser(%s): %w", userID, err)
	}
	return rows, total, nil
}

func (r *EvalWatchlistRepo) SetForUser(ctx context.Context, userID string, agentNames []string) ([]*EvalAgentWatchlist, error) {
	now := time.Now().UnixMilli()
	var rows []*EvalAgentWatchlist
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		rows = make([]*EvalAgentWatchlist, 0, len(agentNames))
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM eval_agent_watchlist WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("clear watchlist: %w", err)
		}
		seen := make(map[string]struct{}, len(agentNames))
		position := 0
		for _, name := range agentNames {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			row := &EvalAgentWatchlist{
				ID:          idgen.New(),
				UserID:      userID,
				AgentName:   name,
				Position:    position,
				CreatedTime: now,
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO eval_agent_watchlist (id, user_id, agent_name, position, created_time)
				 VALUES (?, ?, ?, ?, ?)`,
				row.ID, row.UserID, row.AgentName, row.Position, row.CreatedTime); err != nil {
				return fmt.Errorf("insert watchlist row: %w", err)
			}
			rows = append(rows, row)
			position++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("EvalWatchlistRepo.SetForUser(%s): %w", userID, err)
	}
	return rows, nil
}
