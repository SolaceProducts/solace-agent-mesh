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

type MCPConnectorOAuthState struct {
	StateHash   string `db:"state_hash"`
	SessionID   string `db:"session_id"`
	OwnerUserID string `db:"owner_user_id"`
	Data        string `db:"data"`
	AccessToken string `db:"access_token"`
	ExpiresAt   int64  `db:"expires_at"`
	CreatedTime int64  `db:"created_time"`
}

const mcpConnectorOAuthStateCols = `state_hash, session_id, owner_user_id, data, access_token, expires_at, created_time`

type MCPConnectorOAuthStateRepo struct {
	db *DB
}

func NewMCPConnectorOAuthStateRepo(db *DB) *MCPConnectorOAuthStateRepo {
	return &MCPConnectorOAuthStateRepo{db: db}
}

func (r *MCPConnectorOAuthStateRepo) Insert(ctx context.Context, row MCPConnectorOAuthState) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO mcp_connector_oauth_states (`+mcpConnectorOAuthStateCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		row.StateHash, row.SessionID, row.OwnerUserID, row.Data, row.AccessToken, row.ExpiresAt, row.CreatedTime)
	if err != nil {
		return fmt.Errorf("MCPConnectorOAuthStateRepo.Insert: %w", err)
	}
	return nil
}

func (r *MCPConnectorOAuthStateRepo) Get(ctx context.Context, stateHash string) (*MCPConnectorOAuthState, error) {
	row, err := dbutil.QueryRow[MCPConnectorOAuthState](ctx, r.db,
		`SELECT `+mcpConnectorOAuthStateCols+` FROM mcp_connector_oauth_states WHERE state_hash = ?`,
		stateHash)
	if err != nil {
		return nil, fmt.Errorf("MCPConnectorOAuthStateRepo.Get: %w", err)
	}
	return row, nil
}

func (r *MCPConnectorOAuthStateRepo) SetAccessToken(ctx context.Context, stateHash, accessToken string, nowMs int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE mcp_connector_oauth_states SET access_token = ? WHERE state_hash = ? AND access_token = '' AND expires_at > ?`,
		accessToken, stateHash, nowMs)
	if err != nil {
		return false, fmt.Errorf("MCPConnectorOAuthStateRepo.SetAccessToken: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("MCPConnectorOAuthStateRepo.SetAccessToken: %w", err)
	}
	return n == 1, nil
}

func (r *MCPConnectorOAuthStateRepo) FindBySession(ctx context.Context, sessionID, ownerUserID string, nowMs int64) (*MCPConnectorOAuthState, error) {
	row, err := dbutil.QueryRow[MCPConnectorOAuthState](ctx, r.db,
		`SELECT `+mcpConnectorOAuthStateCols+` FROM mcp_connector_oauth_states WHERE session_id = ? AND owner_user_id = ? AND expires_at > ?`,
		sessionID, ownerUserID, nowMs)
	if err != nil {
		return nil, fmt.Errorf("MCPConnectorOAuthStateRepo.FindBySession: %w", err)
	}
	return row, nil
}

func (r *MCPConnectorOAuthStateRepo) TakeBySession(ctx context.Context, sessionID string, nowMs int64) (*MCPConnectorOAuthState, error) {
	row, err := dbutil.QueryRow[MCPConnectorOAuthState](ctx, r.db,
		`DELETE FROM mcp_connector_oauth_states WHERE session_id = ? AND expires_at > ? RETURNING `+mcpConnectorOAuthStateCols,
		sessionID, nowMs)
	if err != nil {
		return nil, fmt.Errorf("MCPConnectorOAuthStateRepo.TakeBySession: %w", err)
	}
	return row, nil
}

func (r *MCPConnectorOAuthStateRepo) Delete(ctx context.Context, stateHash string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM mcp_connector_oauth_states WHERE state_hash = ?`, stateHash); err != nil {
		return fmt.Errorf("MCPConnectorOAuthStateRepo.Delete: %w", err)
	}
	return nil
}

func (r *MCPConnectorOAuthStateRepo) DeleteExpired(ctx context.Context, nowMs int64) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM mcp_connector_oauth_states WHERE expires_at <= ?`, nowMs); err != nil {
		return fmt.Errorf("MCPConnectorOAuthStateRepo.DeleteExpired: %w", err)
	}
	return nil
}
