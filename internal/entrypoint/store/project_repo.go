// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type ProjectRepo struct {
	db *DB
}

func NewProjectRepo(db *DB) *ProjectRepo {
	return &ProjectRepo{db: db}
}

func (r *ProjectRepo) Create(ctx context.Context, p *Project) error {
	if p.ID == "" {
		p.ID = idgen.New()
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().UnixMilli()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, user_id, description, system_prompt, default_agent_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.UserID, p.Description, p.SystemPrompt, p.DefaultAgentID, p.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("ProjectRepo.Create(%s): %w", p.ID, err)
	}
	return nil
}

func (r *ProjectRepo) NameExistsForUser(ctx context.Context, name, userID, excludeID string) (bool, error) {
	query := `SELECT COUNT(*) FROM projects WHERE user_id = ? AND LOWER(name) = LOWER(?) AND deleted_at IS NULL`
	args := []any{userID, name}
	if excludeID != "" {
		query += " AND id != ?"
		args = append(args, excludeID)
	}
	count, err := dbutil.QueryVal[int](ctx, r.db, query, args...)
	if err != nil {
		return false, fmt.Errorf("ProjectRepo.NameExistsForUser(%s): %w", name, err)
	}
	return count > 0, nil
}

func (r *ProjectRepo) GetByID(ctx context.Context, id string) (*Project, error) {
	return dbutil.QueryRow[Project](ctx, r.db,
		`SELECT id, name, user_id, description, system_prompt, default_agent_id, is_pinned,
			created_at, updated_at, deleted_at, deleted_by
		 FROM projects WHERE id = ? AND deleted_at IS NULL`, id)
}

func (r *ProjectRepo) GetByIDs(ctx context.Context, ids []string) ([]*Project, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("ProjectRepo.GetByIDs: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT id, name, user_id, description, system_prompt, default_agent_id, is_pinned,
			created_at, updated_at, deleted_at, deleted_by
		 FROM projects WHERE id IN (%s) AND deleted_at IS NULL`, ph,
	)
	return dbutil.QueryRowPtrs[Project](ctx, r.db, query, args...)
}

func (r *ProjectRepo) ListByUser(ctx context.Context, userID string) ([]*Project, error) {
	return dbutil.QueryRowPtrs[Project](ctx, r.db,
		`SELECT id, name, user_id, description, system_prompt, default_agent_id, is_pinned,
			created_at, updated_at, deleted_at, deleted_by
		 FROM projects WHERE user_id = ? AND deleted_at IS NULL
		 ORDER BY created_at DESC`, userID)
}

func (r *ProjectRepo) ListAccessible(ctx context.Context, userID string, sharedIDs []string) ([]*Project, error) {
	if len(sharedIDs) == 0 {
		return r.ListByUser(ctx, userID)
	}

	placeholders := make([]string, len(sharedIDs))
	args := make([]any, 0, 1+len(sharedIDs))
	args = append(args, userID)
	for i, id := range sharedIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		`SELECT id, name, user_id, description, system_prompt, default_agent_id, is_pinned,
			created_at, updated_at, deleted_at, deleted_by
		 FROM projects
		 WHERE deleted_at IS NULL AND (user_id = ? OR id IN (%s))
		 ORDER BY created_at DESC`,
		strings.Join(placeholders, ", "),
	)

	return dbutil.QueryRowPtrs[Project](ctx, r.db, query, args...)
}

func (r *ProjectRepo) SoftDeleteSessionsByProjectForUsers(ctx context.Context, projectID string, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	placeholders := make([]string, len(userIDs))
	args := make([]any, 0, 3+len(userIDs))
	args = append(args, now, now, projectID)
	for i, uid := range userIDs {
		placeholders[i] = "?"
		args = append(args, uid)
	}
	query := fmt.Sprintf(
		`UPDATE sessions SET deleted_at = ?, updated_time = ?
		 WHERE project_id = ? AND user_id IN (%s) AND deleted_at IS NULL`,
		strings.Join(placeholders, ", "),
	)
	_, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("ProjectRepo.SoftDeleteSessionsByProjectForUsers(%s): %w", projectID, err)
	}
	return nil
}

func (r *ProjectRepo) Update(ctx context.Context, p *Project) error {
	now := time.Now().UnixMilli()
	p.UpdatedAt = &now
	result, err := r.db.ExecContext(ctx,
		`UPDATE projects SET name = ?, description = ?, system_prompt = ?, default_agent_id = ?, is_pinned = ?, updated_at = ?
		 WHERE id = ? AND deleted_at IS NULL`,
		p.Name, p.Description, p.SystemPrompt, p.DefaultAgentID, dbutil.BoolToInt(p.IsPinned), now, p.ID,
	)
	if err != nil {
		return fmt.Errorf("ProjectRepo.Update(%s): %w", p.ID, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ProjectRepo) SoftDelete(ctx context.Context, id, deletedBy string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE projects SET deleted_at = ?, deleted_by = ? WHERE id = ? AND deleted_at IS NULL`,
		now, deletedBy, id,
	)
	if err != nil {
		return fmt.Errorf("ProjectRepo.SoftDelete(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *ProjectRepo) SoftDeleteSessionsByProject(ctx context.Context, projectID, deletedBy string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET deleted_at = ?, deleted_by = ?, updated_time = ?
		 WHERE project_id = ? AND deleted_at IS NULL`,
		now, deletedBy, now, projectID,
	)
	if err != nil {
		return fmt.Errorf("ProjectRepo.SoftDeleteSessionsByProject(%s): %w", projectID, err)
	}
	return nil
}
