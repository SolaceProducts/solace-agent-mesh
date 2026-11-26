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

type Preference struct {
	UserID      string `db:"user_id"`
	ChannelType string `db:"channel_type"`
	Destination string `db:"destination"`
	Enabled     int    `db:"enabled"`
	UpdatedAt   int64  `db:"updated_at"`
}

const prefKeySeparator = "\x00"

func PrefKey(channelType, destination string) string {
	return channelType + prefKeySeparator + destination
}

type PreferenceRepo struct {
	db *DB
}

func NewPreferenceRepo(db *DB) *PreferenceRepo { return &PreferenceRepo{db: db} }

func (r *PreferenceRepo) List(ctx context.Context, userID string) ([]Preference, error) {
	return dbutil.QueryRows[Preference](ctx,
		r.db,
		`SELECT user_id, channel_type, destination, enabled, updated_at
		 FROM notification_preferences WHERE user_id = ? ORDER BY channel_type, destination`,
		userID,
	)
}

func (r *PreferenceRepo) DisabledChannels(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := dbutil.QueryRows[Preference](ctx,
		r.db,
		`SELECT user_id, channel_type, destination, enabled, updated_at
		 FROM notification_preferences WHERE user_id = ? AND enabled = 0`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query disabled channels: %w", err)
	}
	disabled := make(map[string]bool, len(rows))
	for _, p := range rows {
		disabled[PrefKey(p.ChannelType, p.Destination)] = true
	}
	return disabled, nil
}

func (r *PreferenceRepo) Set(ctx context.Context, userID, channelType, destination string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notification_preferences (user_id, channel_type, destination, enabled, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, channel_type, destination)
		 DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		userID, channelType, destination, dbutil.BoolToInt(enabled), nowMillis(),
	)
	if err != nil {
		return fmt.Errorf("set preference: %w", err)
	}
	return nil
}
