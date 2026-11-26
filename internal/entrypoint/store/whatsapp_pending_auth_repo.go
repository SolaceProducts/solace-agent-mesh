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

const (
	PendingAuthKindLogin   = "login"
	PendingAuthKindConsent = "consent"
)

type WhatsAppPendingAuth struct {
	GatewayID     string `db:"gateway_id"`
	SenderID      string `db:"sender_id"`
	Kind          string `db:"kind"`
	LookupKey     string `db:"lookup_key"`
	LookupValueFP string `db:"lookup_value_fp"`
	OutboundID    string `db:"outbound_id"`
	PromptWamid   string `db:"prompt_wamid"`
	ExpiresAt     int64  `db:"expires_at"`
	CreatedAt     int64  `db:"created_at"`
	UpdatedAt     int64  `db:"updated_at"`
}

type WhatsAppPendingAuthRepo struct {
	db *DB
}

func NewWhatsAppPendingAuthRepo(db *DB) *WhatsAppPendingAuthRepo {
	return &WhatsAppPendingAuthRepo{db: db}
}

const whatsAppPendingAuthColumns = `gateway_id, sender_id, kind, lookup_key, lookup_value_fp,
	outbound_id, prompt_wamid, expires_at, created_at, updated_at`

func (r *WhatsAppPendingAuthRepo) Live(ctx context.Context, gatewayID, senderID string, nowMs int64) (*WhatsAppPendingAuth, error) {
	row, err := dbutil.QueryRow[WhatsAppPendingAuth](ctx, r.db,
		`SELECT `+whatsAppPendingAuthColumns+` FROM whatsapp_pending_auth
		 WHERE gateway_id = ? AND sender_id = ? AND expires_at > ?`, gatewayID, senderID, nowMs)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppPendingAuthRepo.Live: %w", err)
	}
	return row, nil
}

func (r *WhatsAppPendingAuthRepo) Upsert(ctx context.Context, q dbutil.Querier, row *WhatsAppPendingAuth) error {
	if q == nil {
		q = r.db
	}
	insertSQL := `INSERT INTO whatsapp_pending_auth (` + whatsAppPendingAuthColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "gateway_id, sender_id",
		[]string{"kind", "lookup_key", "lookup_value_fp", "outbound_id", "prompt_wamid", "expires_at", "updated_at"})
	if _, err := q.ExecContext(ctx, upsertSQL,
		row.GatewayID, row.SenderID, row.Kind, row.LookupKey, row.LookupValueFP,
		row.OutboundID, row.PromptWamid, row.ExpiresAt, row.CreatedAt, row.UpdatedAt); err != nil {
		return fmt.Errorf("WhatsAppPendingAuthRepo.Upsert kind=%s: %w", row.Kind, err)
	}
	return nil
}

func (r *WhatsAppPendingAuthRepo) Clear(ctx context.Context, q dbutil.Querier, gatewayID, senderID string) error {
	if q == nil {
		q = r.db
	}
	if _, err := q.ExecContext(ctx,
		`DELETE FROM whatsapp_pending_auth WHERE gateway_id = ? AND sender_id = ?`, gatewayID, senderID); err != nil {
		return fmt.Errorf("WhatsAppPendingAuthRepo.Clear: %w", err)
	}
	return nil
}

func (r *WhatsAppPendingAuthRepo) DeleteExpired(ctx context.Context, nowMs int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM whatsapp_pending_auth WHERE expires_at <= ?`, nowMs)
	if err != nil {
		return 0, fmt.Errorf("WhatsAppPendingAuthRepo.DeleteExpired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

const DefaultPendingAuthTTL = 24 * time.Hour
