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

type SharedArtifactRepo struct {
	db *DB
}

func NewSharedArtifactRepo(db *DB) *SharedArtifactRepo {
	return &SharedArtifactRepo{db: db}
}

func (r *SharedArtifactRepo) Create(ctx context.Context, a *SharedArtifact) error {
	if a.CreatedTime == 0 {
		a.CreatedTime = time.Now().UnixMilli()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO shared_artifacts (share_id, artifact_uri, artifact_version, is_public, created_time)
		 VALUES (?, ?, ?, ?, ?)`,
		a.ShareID, a.ArtifactURI, a.ArtifactVersion, dbutil.BoolToInt(a.IsPublic), a.CreatedTime,
	)
	if err != nil {
		return fmt.Errorf("SharedArtifactRepo.Create(%s): %w", a.ShareID, err)
	}
	return nil
}

func (r *SharedArtifactRepo) ListByShareID(ctx context.Context, shareID string) ([]SharedArtifact, error) {
	result, err := dbutil.QueryRows[SharedArtifact](ctx, r.db,
		`SELECT share_id, artifact_uri, artifact_version, is_public, created_time
		 FROM shared_artifacts WHERE share_id = ?`, shareID)
	if err != nil {
		return nil, fmt.Errorf("SharedArtifactRepo.ListByShareID(%s): %w", shareID, err)
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func (r *SharedArtifactRepo) DeleteByShareID(ctx context.Context, shareID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM shared_artifacts WHERE share_id = ?`, shareID,
	)
	if err != nil {
		return fmt.Errorf("SharedArtifactRepo.DeleteByShareID(%s): %w", shareID, err)
	}
	return nil
}
