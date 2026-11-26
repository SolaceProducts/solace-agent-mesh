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

type BuilderSessionResource struct {
	Actor            string `db:"actor"`
	SessionID        string `db:"session_id"`
	Kind             string `db:"kind"`
	ResourceID       string `db:"resource_id"`
	CreatedBySession bool   `db:"created_by_session"`
	CreatedTime      int64  `db:"created_time"`
	UpdatedTime      int64  `db:"updated_time"`
}

type BuilderSessionResourceRepo struct {
	db *DB
}

func NewBuilderSessionResourceRepo(db *DB) *BuilderSessionResourceRepo {
	return &BuilderSessionResourceRepo{db: db}
}

const builderSessionResourceCols = `actor, session_id, kind, resource_id, created_by_session, created_time, updated_time`

func (r *BuilderSessionResourceRepo) Record(ctx context.Context, written []BuilderSessionResource) error {
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		for _, w := range written {
			created := 0
			if w.CreatedBySession {
				created = 1
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO builder_session_resources (`+builderSessionResourceCols+`)
				 VALUES (?, ?, ?, ?, ?, ?, ?)
				 ON CONFLICT (actor, session_id, kind, resource_id) DO UPDATE SET
				   created_by_session = CASE WHEN builder_session_resources.created_by_session = 1 OR EXCLUDED.created_by_session = 1 THEN 1 ELSE 0 END,
				   updated_time = EXCLUDED.updated_time`,
				w.Actor, w.SessionID, w.Kind, w.ResourceID, created, now, now)
			if err != nil {
				return fmt.Errorf("BuilderSessionResourceRepo.Record(%s, %s): %w", w.Kind, w.ResourceID, err)
			}
		}
		return nil
	})
}

func (r *BuilderSessionResourceRepo) ListForSession(ctx context.Context, actor, sessionID string) ([]BuilderSessionResource, error) {
	rows, err := dbutil.QueryRows[BuilderSessionResource](ctx, r.db,
		`SELECT `+builderSessionResourceCols+` FROM builder_session_resources
		 WHERE actor = ? AND session_id = ? ORDER BY created_time, resource_id`, actor, sessionID)
	if err != nil {
		return nil, fmt.Errorf("BuilderSessionResourceRepo.ListForSession(%s): %w", sessionID, err)
	}
	return rows, nil
}

func (r *BuilderSessionResourceRepo) Forget(ctx context.Context, kind, resourceID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM builder_session_resources WHERE kind = ? AND resource_id = ?`, kind, resourceID); err != nil {
		return fmt.Errorf("BuilderSessionResourceRepo.Forget(%s, %s): %w", kind, resourceID, err)
	}
	return nil
}
