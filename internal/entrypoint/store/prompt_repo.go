// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type PromptRepo struct {
	db *DB
}

func NewPromptRepo(db *DB) *PromptRepo {
	return &PromptRepo{db: db}
}

func (r *PromptRepo) Create(ctx context.Context, p *Prompt) error {
	if p.ID == "" {
		p.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO prompts (id, prompt_text, group_id, user_id, version, name, description, category, command, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.PromptText, p.GroupID, p.UserID, p.Version, p.Name, p.Description, p.Category, p.Command, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("PromptRepo.Create(%s): %w", p.ID, err)
	}
	return nil
}

func (r *PromptRepo) GetByID(ctx context.Context, id string) (*Prompt, error) {
	return dbutil.QueryRow[Prompt](ctx, r.db,
		`SELECT id, prompt_text, group_id, user_id, version, name, description, category, command, created_at, updated_at
		 FROM prompts WHERE id = ?`, id)
}

func (r *PromptRepo) GetByIDs(ctx context.Context, ids []string) ([]*Prompt, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("PromptRepo.GetByIDs: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT id, prompt_text, group_id, user_id, version, name, description, category, command, created_at, updated_at
		 FROM prompts WHERE id IN (%s)`, ph,
	)
	return dbutil.QueryRowPtrs[Prompt](ctx, r.db, query, args...)
}

func (r *PromptRepo) ListByGroup(ctx context.Context, groupID string) ([]*Prompt, error) {
	return dbutil.QueryRowPtrs[Prompt](ctx, r.db,
		`SELECT id, prompt_text, group_id, user_id, version, name, description, category, command, created_at, updated_at
		 FROM prompts WHERE group_id = ? ORDER BY created_at DESC`, groupID)
}

func (r *PromptRepo) Update(ctx context.Context, p *Prompt) error {
	p.UpdatedAt = time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE prompts SET prompt_text = ?, name = ?, description = ?, category = ?, command = ?, updated_at = ?
		 WHERE id = ?`,
		p.PromptText, p.Name, p.Description, p.Category, p.Command, p.UpdatedAt, p.ID,
	)
	if err != nil {
		return fmt.Errorf("PromptRepo.Update(%s): %w", p.ID, err)
	}
	return nil
}

func (r *PromptRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM prompts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("PromptRepo.Delete(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *PromptRepo) MaxVersion(ctx context.Context, groupID string) (int, error) {
	maxVer, err := dbutil.QueryVal[sql.NullInt64](ctx, r.db,
		`SELECT MAX(version) FROM prompts WHERE group_id = ?`, groupID)
	if err != nil {
		return 0, fmt.Errorf("PromptRepo.MaxVersion(%s): %w", groupID, err)
	}
	if !maxVer.Valid {
		return 0, nil
	}
	return int(maxVer.Int64), nil
}

func (r *PromptRepo) GetLatestExcluding(ctx context.Context, groupID, excludeID string) (*Prompt, error) {
	return dbutil.QueryRow[Prompt](ctx, r.db,
		`SELECT id, prompt_text, group_id, user_id, version, name, description, category, command, created_at, updated_at
		 FROM prompts WHERE group_id = ? AND id != ? ORDER BY created_at DESC LIMIT 1`,
		groupID, excludeID)
}

func (r *PromptRepo) CountPrompts(ctx context.Context, groupID string) (int, error) {
	return dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM prompts WHERE group_id = ?`, groupID)
}

func (r *PromptRepo) ListCommandsByUser(ctx context.Context, userID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT command FROM prompt_groups WHERE user_id = ? AND command IS NOT NULL`, userID)
}
