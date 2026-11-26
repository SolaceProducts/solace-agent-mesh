// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type UserChannel struct {
	UserID           string `db:"user_id"`
	ChannelType      string `db:"channel_type"`
	Address          string `db:"address"`
	Email            string `db:"email"`
	GatewayID        string `db:"gateway_id"`
	ConfirmedAddress string `db:"confirmed_address"`
	ConfirmedAt      int64  `db:"confirmed_at"`
	PendingExpiresAt int64  `db:"pending_expires_at"`
	PendingAttempts  int    `db:"pending_attempts"`
	UpdatedAt        int64  `db:"updated_at"`
}

func (uc UserChannel) Confirmed() bool { return uc.ConfirmedAddress != "" }

func (uc UserChannel) CodeOutstandingAt(nowMillis int64) bool { return uc.PendingExpiresAt > nowMillis }

func (uc UserChannel) CodeLockedAt(nowMillis int64, maxAttempts int) bool {
	return uc.CodeOutstandingAt(nowMillis) && uc.PendingAttempts >= maxAttempts
}

func (uc UserChannel) CodeUsableAt(nowMillis int64, maxAttempts int) bool {
	return uc.CodeOutstandingAt(nowMillis) && uc.PendingAttempts < maxAttempts
}

const userChannelColumns = `user_id, channel_type, address, email, gateway_id,
	confirmed_address, confirmed_at, pending_expires_at, pending_attempts, updated_at`

var ErrNoCandidateAddress = errors.New("no candidate address for this channel")

type ConfirmOutcome int

const (
	ConfirmInvalid ConfirmOutcome = iota
	ConfirmOK
	ConfirmAlreadyConfirmed
	ConfirmExpired
	ConfirmLocked
)

type UserChannelRepo struct {
	db *DB
}

func NewUserChannelRepo(db *DB) *UserChannelRepo { return &UserChannelRepo{db: db} }

func (r *UserChannelRepo) Upsert(ctx context.Context, uc UserChannel) error {
	if uc.UserID == "" || uc.ChannelType == "" {
		return fmt.Errorf("upsert user channel: user_id and channel_type are required")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notify_user_channels
		   (user_id, channel_type, address, email, gateway_id, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, channel_type) DO UPDATE SET
		   address    = excluded.address,
		   email      = excluded.email,
		   gateway_id = excluded.gateway_id,
		   updated_at = excluded.updated_at`,
		uc.UserID, uc.ChannelType, uc.Address, uc.Email, uc.GatewayID, nowMillis(),
	)
	if err != nil {
		return fmt.Errorf("upsert user channel: %w", err)
	}
	return nil
}

func (r *UserChannelRepo) ConfirmAddress(ctx context.Context, userID, channelType, address string) error {
	if userID == "" || channelType == "" || address == "" {
		return fmt.Errorf("confirm address: user_id, channel_type and address are required")
	}
	now := nowMillis()
	confirmed, err := r.affected(ctx,
		`UPDATE notify_user_channels
		   SET confirmed_address = ?, confirmed_at = ?,
		       pending_address = '', pending_code_hash = '', pending_expires_at = 0,
		       pending_attempts = 0, updated_at = ?
		 WHERE user_id = ? AND channel_type = ?`,
		address, now, now, userID, channelType)
	if err != nil {
		return fmt.Errorf("confirm address: %w", err)
	}
	if !confirmed {
		return ErrNoCandidateAddress
	}
	return nil
}

func (r *UserChannelRepo) ClearConfirmation(ctx context.Context, userID, channelType string) error {
	if userID == "" || channelType == "" {
		return fmt.Errorf("clear confirmation: user_id and channel_type are required")
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE notify_user_channels
		   SET confirmed_address = '', confirmed_at = 0,
		       pending_address = '', pending_code_hash = '', pending_expires_at = 0,
		       pending_attempts = 0, updated_at = ?
		 WHERE user_id = ? AND channel_type = ?`,
		nowMillis(), userID, channelType)
	if err != nil {
		return fmt.Errorf("clear confirmation: %w", err)
	}
	return nil
}

func (r *UserChannelRepo) StartConfirmation(ctx context.Context, userID, channelType, address, codeHash string, expiresAt int64) error {
	if userID == "" || channelType == "" || address == "" || codeHash == "" {
		return fmt.Errorf("start confirmation: user_id, channel_type, address and code hash are required")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE notify_user_channels
		   SET pending_address = ?, pending_code_hash = ?, pending_expires_at = ?,
		       pending_attempts = 0, updated_at = ?
		 WHERE user_id = ? AND channel_type = ?`,
		address, codeHash, expiresAt, nowMillis(), userID, channelType,
	)
	if err != nil {
		return fmt.Errorf("start confirmation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("start confirmation: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNoCandidateAddress
	}
	return nil
}

type pendingState struct {
	CodeHash         string `db:"pending_code_hash"`
	ExpiresAt        int64  `db:"pending_expires_at"`
	Attempts         int    `db:"pending_attempts"`
	ConfirmedAddress string `db:"confirmed_address"`
}

func (r *UserChannelRepo) ConfirmByCode(ctx context.Context, userID, channelType, codeHash string, maxAttempts int) (ConfirmOutcome, error) {
	if userID == "" || channelType == "" || codeHash == "" {
		return ConfirmInvalid, fmt.Errorf("confirm by code: user_id, channel_type and code hash are required")
	}
	now := nowMillis()
	confirmed, err := r.affected(ctx,
		`UPDATE notify_user_channels
		   SET confirmed_address = pending_address, confirmed_at = ?,
		       pending_address = '', pending_code_hash = '', pending_expires_at = 0,
		       pending_attempts = 0, updated_at = ?
		 WHERE user_id = ? AND channel_type = ?
		   AND pending_code_hash <> '' AND pending_code_hash = ?
		   AND pending_expires_at > ? AND pending_attempts < ?`,
		now, now, userID, channelType, codeHash, now, maxAttempts)
	if err != nil {
		return ConfirmInvalid, fmt.Errorf("confirm by code: %w", err)
	}
	if confirmed {
		return ConfirmOK, nil
	}

	counted, err := r.affected(ctx,
		`UPDATE notify_user_channels SET pending_attempts = pending_attempts + 1
		 WHERE user_id = ? AND channel_type = ?
		   AND pending_code_hash <> '' AND pending_code_hash <> ?
		   AND pending_expires_at > ? AND pending_attempts < ?`,
		userID, channelType, codeHash, now, maxAttempts)
	if err != nil {
		return ConfirmInvalid, fmt.Errorf("confirm by code: count attempt: %w", err)
	}
	if counted {
		return ConfirmInvalid, nil
	}

	state, err := dbutil.QueryRow[pendingState](ctx, r.db,
		`SELECT pending_code_hash, pending_expires_at, pending_attempts, confirmed_address
		 FROM notify_user_channels WHERE user_id = ? AND channel_type = ?`,
		userID, channelType)
	switch {
	case err != nil:
		return ConfirmInvalid, fmt.Errorf("confirm by code: read pending state: %w", err)
	case state == nil:
		return ConfirmExpired, nil
	case state.CodeHash == "" && state.ConfirmedAddress != "":
		return ConfirmAlreadyConfirmed, nil
	case state.CodeHash != "" && state.ExpiresAt > now && state.Attempts >= maxAttempts:
		return ConfirmLocked, nil
	default:
		return ConfirmExpired, nil
	}
}

func (r *UserChannelRepo) affected(ctx context.Context, query string, args ...any) (bool, error) {
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n > 0, nil
}

func (r *UserChannelRepo) GetByType(ctx context.Context, userID, channelType string) (*UserChannel, error) {
	uc, err := dbutil.QueryRow[UserChannel](ctx, r.db,
		`SELECT `+userChannelColumns+`
		 FROM notify_user_channels WHERE user_id = ? AND channel_type = ?`,
		userID, channelType)
	if err != nil {
		return nil, fmt.Errorf("get user channel: %w", err)
	}
	return uc, nil
}

func (r *UserChannelRepo) ListByUser(ctx context.Context, userID string) ([]UserChannel, error) {
	rows, err := dbutil.QueryRows[UserChannel](ctx, r.db,
		`SELECT `+userChannelColumns+`
		 FROM notify_user_channels WHERE user_id = ? ORDER BY updated_at DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list user channels: %w", err)
	}
	return rows, nil
}
