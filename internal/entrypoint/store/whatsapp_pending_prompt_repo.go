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

type WhatsAppPendingPrompt struct {
	GatewayID   string `db:"gateway_id"`
	SenderID    string `db:"sender_id"`
	TaskID      string `db:"task_id"`
	RequestID   string `db:"request_id"`
	AgentName   string `db:"agent_name"`
	UserID      string `db:"user_id"`
	SessionID   string `db:"session_id"`
	SurfaceJSON string `db:"surface_json"`
	OutboundID  string `db:"outbound_id"`
	PromptWamid string `db:"prompt_wamid"`
	ExpiresAt   int64  `db:"expires_at"`
	CreatedAt   int64  `db:"created_at"`
	UpdatedAt   int64  `db:"updated_at"`
}

type WhatsAppPendingPromptRepo struct {
	db *DB
}

func NewWhatsAppPendingPromptRepo(db *DB) *WhatsAppPendingPromptRepo {
	return &WhatsAppPendingPromptRepo{db: db}
}

const whatsAppPendingPromptColumns = `gateway_id, sender_id, task_id, request_id, agent_name,
		user_id, session_id, surface_json, outbound_id, prompt_wamid, expires_at, created_at, updated_at`

func (r *WhatsAppPendingPromptRepo) Upsert(ctx context.Context, q dbutil.Querier, row *WhatsAppPendingPrompt) (displaced *WhatsAppPendingPrompt, err error) {
	if q == nil {
		q = r.db
	}
	displaced, err = r.getWith(ctx, q, row.GatewayID, row.SenderID)
	if err != nil {
		return nil, err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO whatsapp_pending_prompts (`+whatsAppPendingPromptColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (gateway_id, sender_id) DO UPDATE SET
			task_id = excluded.task_id,
			request_id = excluded.request_id,
			agent_name = excluded.agent_name,
			user_id = excluded.user_id,
			session_id = excluded.session_id,
			surface_json = excluded.surface_json,
			outbound_id = excluded.outbound_id,
			prompt_wamid = excluded.prompt_wamid,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at`,
		row.GatewayID, row.SenderID, row.TaskID, row.RequestID, row.AgentName,
		row.UserID, row.SessionID, row.SurfaceJSON, row.OutboundID, row.PromptWamid,
		row.ExpiresAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppPendingPromptRepo.Upsert task %s: %w", row.TaskID, err)
	}
	return displaced, nil
}

func (r *WhatsAppPendingPromptRepo) Get(ctx context.Context, gatewayID, senderID string) (*WhatsAppPendingPrompt, error) {
	return r.getWith(ctx, r.db, gatewayID, senderID)
}

func (r *WhatsAppPendingPromptRepo) getWith(ctx context.Context, q dbutil.Querier, gatewayID, senderID string) (*WhatsAppPendingPrompt, error) {
	row, err := dbutil.QueryRow[WhatsAppPendingPrompt](ctx, q,
		`SELECT `+whatsAppPendingPromptColumns+` FROM whatsapp_pending_prompts
		 WHERE gateway_id = ? AND sender_id = ?`,
		gatewayID, senderID)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppPendingPromptRepo.Get: %w", err)
	}
	return row, nil
}

func (r *WhatsAppPendingPromptRepo) DeleteByTask(ctx context.Context, gatewayID, senderID, taskID string) (int, error) {
	n, err := r.execCount(ctx, r.db,
		`DELETE FROM whatsapp_pending_prompts
		 WHERE gateway_id = ? AND sender_id = ? AND task_id = ?`,
		gatewayID, senderID, taskID)
	if err != nil {
		return 0, fmt.Errorf("WhatsAppPendingPromptRepo.DeleteByTask task %s: %w", taskID, err)
	}
	return n, nil
}

func (r *WhatsAppPendingPromptRepo) DeleteAnswered(ctx context.Context, gatewayID, senderID, taskID, requestID string) (int, error) {
	n, err := r.execCount(ctx, r.db,
		`DELETE FROM whatsapp_pending_prompts
		 WHERE gateway_id = ? AND sender_id = ? AND task_id = ? AND request_id = ?`,
		gatewayID, senderID, taskID, requestID)
	if err != nil {
		return 0, fmt.Errorf("WhatsAppPendingPromptRepo.DeleteAnswered task %s: %w", taskID, err)
	}
	return n, nil
}

func (r *WhatsAppPendingPromptRepo) DeleteByOutbound(ctx context.Context, q dbutil.Querier, gatewayID, senderID, outboundID string) (int, error) {
	if q == nil {
		q = r.db
	}
	n, err := r.execCount(ctx, q,
		`DELETE FROM whatsapp_pending_prompts
		 WHERE gateway_id = ? AND sender_id = ? AND outbound_id = ?`,
		gatewayID, senderID, outboundID)
	if err != nil {
		return 0, fmt.Errorf("WhatsAppPendingPromptRepo.DeleteByOutbound %s: %w", outboundID, err)
	}
	return n, nil
}

func (r *WhatsAppPendingPromptRepo) ClaimExpired(ctx context.Context, gatewayID, senderID, requestID string, nowMs int64) (bool, error) {
	n, err := r.execCount(ctx, r.db,
		`DELETE FROM whatsapp_pending_prompts
		 WHERE gateway_id = ? AND sender_id = ? AND request_id = ? AND expires_at <= ?`,
		gatewayID, senderID, requestID, nowMs)
	if err != nil {
		return false, fmt.Errorf("WhatsAppPendingPromptRepo.ClaimExpired: %w", err)
	}
	return n > 0, nil
}

func (r *WhatsAppPendingPromptRepo) ListExpired(ctx context.Context, gatewayID string, nowMs int64, limit int) ([]WhatsAppPendingPrompt, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := dbutil.QueryRows[WhatsAppPendingPrompt](ctx, r.db,
		`SELECT `+whatsAppPendingPromptColumns+` FROM whatsapp_pending_prompts
		 WHERE gateway_id = ? AND expires_at <= ?
		 ORDER BY expires_at LIMIT ?`,
		gatewayID, nowMs, limit)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppPendingPromptRepo.ListExpired: %w", err)
	}
	return rows, nil
}

func (r *WhatsAppPendingPromptRepo) PruneExpired(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	beforeMs := before.UnixMilli()
	var total int64
	for {
		result, err := r.db.ExecContext(ctx,
			`DELETE FROM whatsapp_pending_prompts WHERE (gateway_id, sender_id) IN (
				SELECT gateway_id, sender_id FROM whatsapp_pending_prompts
				WHERE expires_at < ? LIMIT ?
			)`, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("WhatsAppPendingPromptRepo.PruneExpired: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("WhatsAppPendingPromptRepo.PruneExpired: rows affected: %w", err)
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

func (r *WhatsAppPendingPromptRepo) execCount(ctx context.Context, q dbutil.Querier, query string, args ...any) (int, error) {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
