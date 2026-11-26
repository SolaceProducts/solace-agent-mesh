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

type MCPArtifactLink struct {
	MCPSessionID string `db:"mcp_session_id"`
	URI          string `db:"uri"`
	AgentName    string `db:"agent_name"`
	UserID       string `db:"user_id"`
	SessionID    string `db:"session_id"`
	Filename     string `db:"filename"`
	DisplayName  string `db:"display_name"`
	MIMEType     string `db:"mime_type"`
	Version      *int   `db:"version"`
	CreatedAt    int64  `db:"created_at"`
}

type MCPArtifactLinkRepo struct {
	db *DB
}

func NewMCPArtifactLinkRepo(db *DB) *MCPArtifactLinkRepo {
	return &MCPArtifactLinkRepo{db: db}
}

func (r *MCPArtifactLinkRepo) Put(ctx context.Context, l MCPArtifactLink) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO mcp_artifact_links
		   (mcp_session_id, uri, agent_name, user_id, session_id, filename, display_name, mime_type, version, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (mcp_session_id, uri) DO UPDATE SET
		   agent_name = excluded.agent_name, user_id = excluded.user_id, session_id = excluded.session_id,
		   filename = excluded.filename, display_name = excluded.display_name, mime_type = excluded.mime_type,
		   version = excluded.version, created_at = excluded.created_at`,
		l.MCPSessionID, l.URI, l.AgentName, l.UserID, l.SessionID, l.Filename, l.DisplayName, l.MIMEType, l.Version, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("MCPArtifactLinkRepo.Put: %w", err)
	}
	return nil
}

func (r *MCPArtifactLinkRepo) ListBySession(ctx context.Context, mcpSessionID string) ([]MCPArtifactLink, error) {
	links, err := dbutil.QueryRows[MCPArtifactLink](ctx, r.db,
		`SELECT mcp_session_id, uri, agent_name, user_id, session_id, filename, display_name, mime_type, version, created_at
		 FROM mcp_artifact_links WHERE mcp_session_id = ?`, mcpSessionID)
	if err != nil {
		return nil, fmt.Errorf("MCPArtifactLinkRepo.ListBySession: %w", err)
	}
	return links, nil
}

func (r *MCPArtifactLinkRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	var total int64
	for {
		res, err := r.db.ExecContext(ctx,
			`DELETE FROM mcp_artifact_links
			 WHERE (mcp_session_id, uri) IN (
				SELECT mcp_session_id, uri FROM mcp_artifact_links WHERE created_at < ? LIMIT ?
			 )`, before.UnixMilli(), batchSize)
		if err != nil {
			return total, fmt.Errorf("MCPArtifactLinkRepo.DeleteOlderThan: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("MCPArtifactLinkRepo.DeleteOlderThan rows affected: %w", err)
		}
		total += n
		if n < int64(batchSize) {
			return total, nil
		}
	}
}
