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

type PlatformIdentityConfigRepo struct {
	db *DB
}

func NewPlatformIdentityConfigRepo(db *DB) *PlatformIdentityConfigRepo {
	return &PlatformIdentityConfigRepo{db: db}
}

func (r *PlatformIdentityConfigRepo) Get(ctx context.Context) (*PlatformIdentityConfig, error) {
	return dbutil.QueryRow[PlatformIdentityConfig](ctx, r.db,
		`SELECT id, toolset_id, lookup_key, created_by, updated_by, created_time, updated_time
		 FROM platform_identity_config WHERE id = ?`,
		PlatformIdentityConfigSingletonID,
	)
}

func (r *PlatformIdentityConfigRepo) Set(ctx context.Context, toolsetID *string, lookupKey, actor string) error {
	now := time.Now().UnixMilli()
	insertSQL := `INSERT INTO platform_identity_config (id, toolset_id, lookup_key, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "id",
		[]string{"toolset_id", "lookup_key", "updated_by", "updated_time"})
	if _, err := r.db.ExecContext(ctx, upsertSQL,
		PlatformIdentityConfigSingletonID, toolsetID, lookupKey, actor, actor, now, now,
	); err != nil {
		return fmt.Errorf("upsert platform_identity_config: %w", err)
	}
	return nil
}

func (r *PlatformIdentityConfigRepo) Clear(ctx context.Context, actor string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE platform_identity_config SET toolset_id = NULL, updated_by = ?, updated_time = ? WHERE id = ?`,
		actor, now, PlatformIdentityConfigSingletonID,
	)
	if err != nil {
		return fmt.Errorf("clear platform_identity_config: %w", err)
	}
	return nil
}
