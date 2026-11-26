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

type WhatsAppConversation struct {
	GatewayID     string `db:"gateway_id"`
	SenderID      string `db:"sender_id"`
	WaID          string `db:"wa_id"`
	FromID        string `db:"from_id"`
	LastInboundAt int64  `db:"last_inbound_at"`
	SessionEpoch  int64  `db:"session_epoch"`
	CreatedAt     int64  `db:"created_at"`
	UpdatedAt     int64  `db:"updated_at"`
}

type WhatsAppConversationRepo struct {
	db *DB
}

func NewWhatsAppConversationRepo(db *DB) *WhatsAppConversationRepo {
	return &WhatsAppConversationRepo{db: db}
}

const whatsAppConversationColumns = `gateway_id, sender_id, wa_id, from_id, last_inbound_at,
		session_epoch, created_at, updated_at`

func (r *WhatsAppConversationRepo) RecordInbound(ctx context.Context, row *WhatsAppConversation, idleReset time.Duration) (int64, error) {
	openingEpoch := row.LastInboundAt
	epoch, err := dbutil.QueryVal[int64](ctx, r.db,
		`INSERT INTO whatsapp_conversations (`+whatsAppConversationColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (gateway_id, sender_id) DO UPDATE SET
			wa_id = CASE WHEN excluded.wa_id <> '' THEN excluded.wa_id ELSE whatsapp_conversations.wa_id END,
			from_id = excluded.from_id,
			session_epoch = CASE
				WHEN whatsapp_conversations.session_epoch = 0
					OR excluded.last_inbound_at - whatsapp_conversations.last_inbound_at > ?
					THEN excluded.last_inbound_at
				ELSE whatsapp_conversations.session_epoch END,
			last_inbound_at = excluded.last_inbound_at,
			updated_at = excluded.updated_at
		RETURNING session_epoch`,
		row.GatewayID, row.SenderID, row.WaID, row.FromID, row.LastInboundAt,
		openingEpoch, row.CreatedAt, row.UpdatedAt,
		idleReset.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("WhatsAppConversationRepo.RecordInbound: %w", err)
	}
	if epoch == 0 {
		return 0, fmt.Errorf("WhatsAppConversationRepo.RecordInbound: upsert returned no session epoch")
	}
	return epoch, nil
}

func (r *WhatsAppConversationRepo) Get(ctx context.Context, gatewayID, senderID string) (*WhatsAppConversation, error) {
	row, err := dbutil.QueryRow[WhatsAppConversation](ctx, r.db,
		`SELECT `+whatsAppConversationColumns+` FROM whatsapp_conversations
		 WHERE gateway_id = ? AND sender_id = ?`,
		gatewayID, senderID)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppConversationRepo.Get: %w", err)
	}
	return row, nil
}

func (r *WhatsAppConversationRepo) PruneStale(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	beforeMs := before.UnixMilli()
	var total int64
	for {
		result, err := r.db.ExecContext(ctx,
			`DELETE FROM whatsapp_conversations WHERE (gateway_id, sender_id) IN (
				SELECT gateway_id, sender_id FROM whatsapp_conversations
				WHERE last_inbound_at < ? LIMIT ?
			)`, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("WhatsAppConversationRepo.PruneStale: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("WhatsAppConversationRepo.PruneStale: rows affected: %w", err)
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}
