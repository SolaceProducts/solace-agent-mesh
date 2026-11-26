// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func CellKey(category, channelType, destination string) string {
	return CellPrefix(category) + PrefKey(channelType, destination)
}

func CellPrefix(category string) string { return category + "\x00" }

type Cell struct {
	Category    string
	ChannelType string
	Destination string
	Enabled     bool
}

type CellPreferenceRepo struct {
	db *DB
}

func NewCellPreferenceRepo(db *DB) *CellPreferenceRepo { return &CellPreferenceRepo{db: db} }

type cellRow struct {
	Category    string `db:"category"`
	ChannelType string `db:"channel_type"`
	Destination string `db:"destination"`
}

func (r *CellPreferenceRepo) DisabledCells(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := dbutil.QueryRows[cellRow](ctx, r.db,
		`SELECT category, channel_type, destination FROM notification_cell_preferences
		 WHERE user_id = ? AND enabled = 0`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query disabled cells: %w", err)
	}
	disabled := make(map[string]bool, len(rows))
	for _, row := range rows {
		disabled[CellKey(row.Category, row.ChannelType, row.Destination)] = true
	}
	return disabled, nil
}

type OptOuts struct {
	MutedCategories  map[string]bool
	DisabledChannels map[string]bool
	DisabledCells    map[string]bool
}

func (r *CellPreferenceRepo) ReplaceMatrix(ctx context.Context, userID string, requested []Cell, preserve func(OptOuts) []Cell) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := lockUserCells(ctx, tx, userID); err != nil {
			return err
		}
		var preserved []Cell
		if preserve != nil {
			optOuts, err := readOptOuts(ctx, tx, userID)
			if err != nil {
				return err
			}
			preserved = offCellsNotRequested(preserve(optOuts), requested)
		}
		now := nowMillis()
		clearedCategories := map[string]bool{}
		clearedChannels := map[string]bool{}
		for _, c := range requested {
			if !clearedCategories[c.Category] {
				clearedCategories[c.Category] = true
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM notification_category_preferences WHERE user_id = ? AND category = ?`,
					userID, c.Category); err != nil {
					return fmt.Errorf("clear category mute: %w", err)
				}
			}
			if key := PrefKey(c.ChannelType, c.Destination); !clearedChannels[key] {
				clearedChannels[key] = true
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM notification_preferences WHERE user_id = ? AND channel_type = ? AND destination = ?`,
					userID, c.ChannelType, c.Destination); err != nil {
					return fmt.Errorf("clear channel opt-out: %w", err)
				}
			}
		}
		for _, c := range slices.Concat(requested, preserved) {
			if err := upsertCell(ctx, tx, userID, c, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func offCellsNotRequested(preserved, requested []Cell) []Cell {
	requestedKeys := make(map[string]bool, len(requested))
	for _, c := range requested {
		requestedKeys[CellKey(c.Category, c.ChannelType, c.Destination)] = true
	}
	var off []Cell
	for _, c := range preserved {
		if c.Enabled || requestedKeys[CellKey(c.Category, c.ChannelType, c.Destination)] {
			continue
		}
		off = append(off, c)
	}
	return off
}

func lockUserCells(ctx context.Context, tx *dbutil.Tx, userID string) error {
	if tx.Dialect() != "postgres" {
		return nil
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte("sam-notify-cells:" + userID))
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?)`, int64(h.Sum64())); err != nil {
		return fmt.Errorf("lock user cells: %w", err)
	}
	return nil
}

func readOptOuts(ctx context.Context, tx *dbutil.Tx, userID string) (OptOuts, error) {
	muted, err := disabledKeys(ctx, tx, userID,
		`SELECT category FROM notification_category_preferences WHERE user_id = ? AND enabled = 0`,
		func(cols []string) string { return cols[0] }, 1)
	if err != nil {
		return OptOuts{}, fmt.Errorf("read muted categories: %w", err)
	}
	channels, err := disabledKeys(ctx, tx, userID,
		`SELECT channel_type, destination FROM notification_preferences WHERE user_id = ? AND enabled = 0`,
		func(cols []string) string { return PrefKey(cols[0], cols[1]) }, 2)
	if err != nil {
		return OptOuts{}, fmt.Errorf("read disabled channels: %w", err)
	}
	cells, err := disabledKeys(ctx, tx, userID,
		`SELECT category, channel_type, destination FROM notification_cell_preferences WHERE user_id = ? AND enabled = 0`,
		func(cols []string) string { return CellKey(cols[0], cols[1], cols[2]) }, 3)
	if err != nil {
		return OptOuts{}, fmt.Errorf("read disabled cells: %w", err)
	}
	return OptOuts{MutedCategories: muted, DisabledChannels: channels, DisabledCells: cells}, nil
}

func disabledKeys(ctx context.Context, tx *dbutil.Tx, userID, query string, key func(cols []string) string, columns int) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys := map[string]bool{}
	for rows.Next() {
		cols := make([]string, columns)
		dest := make([]any, columns)
		for i := range cols {
			dest[i] = &cols[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		keys[key(cols)] = true
	}
	return keys, rows.Err()
}

func upsertCell(ctx context.Context, tx *dbutil.Tx, userID string, c Cell, now int64) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notification_cell_preferences (user_id, category, channel_type, destination, enabled, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, category, channel_type, destination)
		 DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		userID, c.Category, c.ChannelType, c.Destination, dbutil.BoolToInt(c.Enabled), now); err != nil {
		return fmt.Errorf("set cell preference: %w", err)
	}
	return nil
}
