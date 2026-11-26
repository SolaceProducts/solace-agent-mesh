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

type ToolAuthStateRepo struct {
	db *DB
}

func NewToolAuthStateRepo(db *DB) *ToolAuthStateRepo {
	return &ToolAuthStateRepo{db: db}
}

type toolAuthStateRow struct {
	Data string `db:"data"`
}

func (r *ToolAuthStateRepo) Put(ctx context.Context, state string, data []byte, expiresAtMs, nowMs int64) error {
	insertSQL := `INSERT INTO tool_auth_state (state, data, expires_at, created_time) VALUES (?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "state",
		[]string{"data", "expires_at", "created_time"})
	if _, err := r.db.ExecContext(ctx, upsertSQL, state, string(data), expiresAtMs, nowMs); err != nil {
		return fmt.Errorf("ToolAuthStateRepo.Put: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM tool_auth_state WHERE expires_at <= ?`, nowMs); err != nil {
		r.db.Logger().With("logger_name", "tool-auth-state-repo").Warn("failed to sweep expired tool auth state", "error", err)
	}
	return nil
}

func (r *ToolAuthStateRepo) Take(ctx context.Context, state string, nowMs int64) ([]byte, bool, error) {
	row, err := dbutil.QueryRow[toolAuthStateRow](ctx, r.db,
		`DELETE FROM tool_auth_state WHERE state = ? AND expires_at > ? RETURNING data`, state, nowMs)
	if err != nil {
		return nil, false, fmt.Errorf("ToolAuthStateRepo.Take: %w", err)
	}
	if row == nil {
		return nil, false, nil
	}
	return []byte(row.Data), true, nil
}
