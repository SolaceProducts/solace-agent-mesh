// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"
)

func IsSeedApplied(ctx context.Context, db *DB, key string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM system_seed_state WHERE seed_key = ?`, key,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("probe seed marker %q: %w", key, err)
	}
	return count > 0, nil
}

func MarkSeedApplied(ctx context.Context, db *DB, key string) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO system_seed_state (seed_key, seeded_at) VALUES (?, ?)
		 ON CONFLICT (seed_key) DO NOTHING`,
		key, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("write seed marker %q: %w", key, err)
	}
	return nil
}
