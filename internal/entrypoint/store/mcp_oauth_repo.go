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

type MCPOAuthRepo struct {
	db *DB
}

func NewMCPOAuthRepo(db *DB) *MCPOAuthRepo {
	return &MCPOAuthRepo{db: db}
}

type mcpOAuthDataRow struct {
	Data string `db:"data"`
}

func (r *MCPOAuthRepo) PutState(ctx context.Context, gatewayID, stateHash, verifierHash, data string, expiresAt, now int64) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO mcp_oauth_state (state_hash, gateway_id, verifier_hash, data, expires_at, created_time) VALUES (?, ?, ?, ?, ?, ?)`,
		stateHash, gatewayID, verifierHash, data, expiresAt, now); err != nil {
		return fmt.Errorf("MCPOAuthRepo.PutState: %w", err)
	}
	return nil
}

func (r *MCPOAuthRepo) TakeState(ctx context.Context, gatewayID, stateHash string, now int64) (string, bool, error) {
	return r.take(ctx, "TakeState",
		`DELETE FROM mcp_oauth_state WHERE state_hash = ? AND gateway_id = ? AND expires_at > ? RETURNING data`,
		stateHash, gatewayID, now)
}

func (r *MCPOAuthRepo) TakeStateByVerifier(ctx context.Context, gatewayID, verifierHash string, now int64) (string, bool, error) {
	return r.take(ctx, "TakeStateByVerifier",
		`DELETE FROM mcp_oauth_state WHERE verifier_hash = ? AND gateway_id = ? AND expires_at > ? RETURNING data`,
		verifierHash, gatewayID, now)
}

func (r *MCPOAuthRepo) CountLiveStates(ctx context.Context, gatewayID string, now int64) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_oauth_state WHERE gateway_id = ? AND expires_at > ?`, gatewayID, now).Scan(&n); err != nil {
		return 0, fmt.Errorf("MCPOAuthRepo.CountLiveStates: %w", err)
	}
	return n, nil
}

func (r *MCPOAuthRepo) PutCode(ctx context.Context, gatewayID, codeHash, data string, expiresAt, now int64) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO mcp_oauth_code (code_hash, gateway_id, data, expires_at, created_time) VALUES (?, ?, ?, ?, ?)`,
		codeHash, gatewayID, data, expiresAt, now); err != nil {
		return fmt.Errorf("MCPOAuthRepo.PutCode: %w", err)
	}
	return nil
}

func (r *MCPOAuthRepo) TakeCode(ctx context.Context, gatewayID, codeHash string, now int64) (string, bool, error) {
	return r.take(ctx, "TakeCode",
		`DELETE FROM mcp_oauth_code WHERE code_hash = ? AND gateway_id = ? AND expires_at > ? RETURNING data`,
		codeHash, gatewayID, now)
}

func (r *MCPOAuthRepo) PutRefresh(ctx context.Context, gatewayID, tokenHash, data string, expiresAt, now int64) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO mcp_oauth_refresh (token_hash, gateway_id, data, expires_at, created_time) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, gatewayID, data, expiresAt, now); err != nil {
		return fmt.Errorf("MCPOAuthRepo.PutRefresh: %w", err)
	}
	return nil
}

func (r *MCPOAuthRepo) GetRefresh(ctx context.Context, gatewayID, tokenHash string, now int64) (string, bool, error) {
	return r.take(ctx, "GetRefresh",
		`SELECT data FROM mcp_oauth_refresh WHERE token_hash = ? AND gateway_id = ? AND expires_at > ?`,
		tokenHash, gatewayID, now)
}

func (r *MCPOAuthRepo) RotateRefresh(ctx context.Context, gatewayID, oldHash, newHash, data string, expiresAt, now int64) (bool, error) {
	rotated := false
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM mcp_oauth_refresh WHERE token_hash = ? AND gateway_id = ? AND expires_at > ?`, oldHash, gatewayID, now)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mcp_oauth_refresh (token_hash, gateway_id, data, expires_at, created_time) VALUES (?, ?, ?, ?, ?)`,
			newHash, gatewayID, data, expiresAt, now); err != nil {
			return err
		}
		rotated = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("MCPOAuthRepo.RotateRefresh: %w", err)
	}
	return rotated, nil
}

func (r *MCPOAuthRepo) DeleteExpired(ctx context.Context, now int64) error {
	for _, table := range []string{"mcp_oauth_state", "mcp_oauth_code", "mcp_oauth_refresh"} {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE expires_at <= ?`, now); err != nil {
			return fmt.Errorf("MCPOAuthRepo.DeleteExpired %s: %w", table, err)
		}
	}
	return nil
}

func (r *MCPOAuthRepo) take(ctx context.Context, op, query string, args ...any) (string, bool, error) {
	row, err := dbutil.QueryRow[mcpOAuthDataRow](ctx, r.db, query, args...)
	if err != nil {
		return "", false, fmt.Errorf("MCPOAuthRepo.%s: %w", op, err)
	}
	if row == nil {
		return "", false, nil
	}
	return row.Data, true, nil
}
