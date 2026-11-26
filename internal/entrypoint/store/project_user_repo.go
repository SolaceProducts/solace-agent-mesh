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
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type ProjectUserRepo struct {
	db *DB
}

func NewProjectUserRepo(db *DB) *ProjectUserRepo {
	return &ProjectUserRepo{db: db}
}

func (r *ProjectUserRepo) AddUser(ctx context.Context, pu *ProjectUser) error {
	if pu.ID == "" {
		pu.ID = idgen.New()
	}
	if pu.AddedAt == 0 {
		pu.AddedAt = time.Now().UnixMilli()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO project_users (id, project_id, user_id, role, added_at, added_by_user_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		pu.ID, pu.ProjectID, pu.UserID, pu.Role, pu.AddedAt, pu.AddedByUserID,
	)
	if err != nil {
		return fmt.Errorf("ProjectUserRepo.AddUser(%s): %w", pu.ID, err)
	}
	return nil
}

func (r *ProjectUserRepo) HasAccess(ctx context.Context, projectID, userID string) (bool, error) {
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM project_users WHERE project_id = ? AND user_id = ?`,
		projectID, userID)
	if err != nil {
		return false, fmt.Errorf("ProjectUserRepo.HasAccess(%s, %s): %w", projectID, userID, err)
	}
	return count > 0, nil
}

func (r *ProjectUserRepo) RemoveByProject(ctx context.Context, projectID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM project_users WHERE project_id = ?`, projectID,
	)
	if err != nil {
		return fmt.Errorf("ProjectUserRepo.RemoveByProject(%s): %w", projectID, err)
	}
	return nil
}
