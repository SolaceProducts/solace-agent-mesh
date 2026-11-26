// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var userKeyedTables = []string{
	"notifications",
	"notify_dedup_keys",
	"notification_preferences",
	"notification_category_preferences",
	"notification_cell_preferences",
	"notify_user_channels",
}

var hasRowsForQuery = func() string {
	exists := make([]string, len(userKeyedTables))
	for i, t := range userKeyedTables {
		exists[i] = "EXISTS (SELECT 1 FROM " + t + " WHERE user_id = ?)"
	}
	return "SELECT CASE WHEN " + strings.Join(exists, " OR ") + " THEN 1 ELSE 0 END"
}()

func (db *DB) HasRowsFor(ctx context.Context, userID string) (bool, error) {
	args := make([]any, len(userKeyedTables))
	for i := range args {
		args[i] = userID
	}
	var found int
	if err := db.QueryRowContext(ctx, hasRowsForQuery, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("check rows for user: %w", err)
	}
	return found == 1, nil
}

func (db *DB) RecordAlias(ctx context.Context, loginID, platformUserID string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO notify_user_aliases (login_id, platform_user_id, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (login_id) DO UPDATE SET
		   platform_user_id = excluded.platform_user_id, updated_at = excluded.updated_at
		 WHERE notify_user_aliases.platform_user_id <> excluded.platform_user_id`,
		loginID, platformUserID, nowMillis(),
	)
	if err != nil {
		return fmt.Errorf("record notify user alias: %w", err)
	}
	return nil
}

func (db *DB) PlatformUserIDFor(ctx context.Context, loginID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT platform_user_id FROM notify_user_aliases WHERE login_id = ?`, loginID,
	).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("look up notify user alias: %w", err)
	}
	return id, nil
}

func (db *DB) IsAliasTarget(ctx context.Context, userID string) (bool, error) {
	var found int
	err := db.QueryRowContext(ctx,
		`SELECT CASE WHEN EXISTS (SELECT 1 FROM notify_user_aliases WHERE platform_user_id = ?) THEN 1 ELSE 0 END`, userID,
	).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check notify user alias target: %w", err)
	}
	return found == 1, nil
}

func (db *DB) AdoptUser(ctx context.Context, from, to string) error {
	if from == "" || to == "" || from == to {
		return nil
	}
	statements := []string{
		`UPDATE notifications SET user_id = ? WHERE user_id = ?`,
		`INSERT INTO notify_dedup_keys (user_id, dedup, created_at)
		 SELECT ?, dedup, created_at FROM notify_dedup_keys WHERE user_id = ?
		 ON CONFLICT (user_id, dedup) DO NOTHING`,
		`INSERT INTO notification_preferences (user_id, channel_type, destination, enabled, updated_at)
		 SELECT ?, channel_type, destination, enabled, updated_at FROM notification_preferences WHERE user_id = ?
		 ON CONFLICT (user_id, channel_type, destination) DO NOTHING`,
		`INSERT INTO notification_category_preferences (user_id, category, enabled, updated_at)
		 SELECT ?, category, enabled, updated_at FROM notification_category_preferences WHERE user_id = ?
		 ON CONFLICT (user_id, category) DO NOTHING`,
		`INSERT INTO notification_cell_preferences (user_id, category, channel_type, destination, enabled, updated_at)
		 SELECT ?, category, channel_type, destination, enabled, updated_at FROM notification_cell_preferences WHERE user_id = ?
		 ON CONFLICT (user_id, category, channel_type, destination) DO NOTHING`,
		adoptUserChannels,
	}
	return db.WithTx(ctx, func(tx *dbutil.Tx) error {
		for _, s := range statements {
			if _, err := tx.ExecContext(ctx, s, to, from); err != nil {
				return fmt.Errorf("adopt notify rows: %w", err)
			}
		}
		return deleteAdoptedLoginRows(ctx, tx, from, to)
	})
}

const adoptUserChannels = `INSERT INTO notify_user_channels (user_id, channel_type, address, email, gateway_id, updated_at,
   confirmed_address, confirmed_at, pending_address, pending_code_hash, pending_expires_at, pending_attempts)
 SELECT ?, channel_type, address, email, gateway_id, updated_at,
   confirmed_address, confirmed_at, pending_address, pending_code_hash, pending_expires_at, pending_attempts
 FROM notify_user_channels WHERE user_id = ?
 ON CONFLICT (user_id, channel_type) DO UPDATE SET
   address    = CASE WHEN ` + newerPresence + ` THEN excluded.address ELSE notify_user_channels.address END,
   email      = CASE WHEN ` + newerPresence + ` THEN excluded.email ELSE notify_user_channels.email END,
   gateway_id = CASE WHEN ` + newerPresence + ` THEN excluded.gateway_id ELSE notify_user_channels.gateway_id END,
   updated_at = CASE WHEN ` + newerPresence + ` THEN excluded.updated_at ELSE notify_user_channels.updated_at END,
   confirmed_address  = CASE WHEN ` + takeConfirmation + ` THEN excluded.confirmed_address ELSE notify_user_channels.confirmed_address END,
   confirmed_at       = CASE WHEN ` + takeConfirmation + ` THEN excluded.confirmed_at ELSE notify_user_channels.confirmed_at END,
   pending_address    = CASE WHEN ` + takeConfirmation + ` THEN excluded.pending_address ELSE notify_user_channels.pending_address END,
   pending_code_hash  = CASE WHEN ` + takeConfirmation + ` THEN excluded.pending_code_hash ELSE notify_user_channels.pending_code_hash END,
   pending_expires_at = CASE WHEN ` + takeConfirmation + ` THEN excluded.pending_expires_at ELSE notify_user_channels.pending_expires_at END,
   pending_attempts   = CASE WHEN ` + takeConfirmation + ` THEN excluded.pending_attempts ELSE notify_user_channels.pending_attempts END`

const (
	newerPresence    = `excluded.updated_at > notify_user_channels.updated_at`
	takeConfirmation = `notify_user_channels.confirmed_address = '' AND (excluded.confirmed_address <> ''
	   OR (notify_user_channels.pending_code_hash = '' AND excluded.pending_code_hash <> ''))`
)

var adoptedLoginRowDeletes = []string{
	`DELETE FROM notify_dedup_keys WHERE user_id = ? AND EXISTS (
	   SELECT 1 FROM notify_dedup_keys t
	   WHERE t.user_id = ? AND t.dedup = notify_dedup_keys.dedup)`,
	`DELETE FROM notification_preferences WHERE user_id = ? AND EXISTS (
	   SELECT 1 FROM notification_preferences t
	   WHERE t.user_id = ? AND t.channel_type = notification_preferences.channel_type
	     AND t.destination = notification_preferences.destination)`,
	`DELETE FROM notification_category_preferences WHERE user_id = ? AND EXISTS (
	   SELECT 1 FROM notification_category_preferences t
	   WHERE t.user_id = ? AND t.category = notification_category_preferences.category)`,
	`DELETE FROM notification_cell_preferences WHERE user_id = ? AND EXISTS (
	   SELECT 1 FROM notification_cell_preferences t
	   WHERE t.user_id = ? AND t.category = notification_cell_preferences.category
	     AND t.channel_type = notification_cell_preferences.channel_type
	     AND t.destination = notification_cell_preferences.destination)`,
	`DELETE FROM notify_user_channels WHERE user_id = ? AND EXISTS (
	   SELECT 1 FROM notify_user_channels t
	   WHERE t.user_id = ? AND t.channel_type = notify_user_channels.channel_type)`,
}

func deleteAdoptedLoginRows(ctx context.Context, q dbutil.Querier, from, to string) error {
	for _, s := range adoptedLoginRowDeletes {
		if _, err := q.ExecContext(ctx, s, from, to); err != nil {
			return fmt.Errorf("adopt notify rows: %w", err)
		}
	}
	return nil
}
