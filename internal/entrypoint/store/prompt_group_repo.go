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

type PromptGroupRepo struct {
	db *DB
}

func NewPromptGroupRepo(db *DB) *PromptGroupRepo {
	return &PromptGroupRepo{db: db}
}

func (r *PromptGroupRepo) Create(ctx context.Context, g *PromptGroup) error {
	if g.ID == "" {
		g.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if g.CreatedAt == 0 {
		g.CreatedAt = now
	}
	if g.UpdatedAt == 0 {
		g.UpdatedAt = now
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO prompt_groups (id, name, description, category, command, user_id, author_name,
			production_prompt_id, is_shared, is_pinned, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Name, g.Description, g.Category, g.Command, g.UserID, g.AuthorName,
		g.ProductionPromptID, dbutil.BoolToInt(g.IsShared), dbutil.BoolToInt(g.IsPinned), g.CreatedAt, g.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("PromptGroupRepo.Create(%s): %w", g.ID, err)
	}
	return nil
}

func (r *PromptGroupRepo) GetByID(ctx context.Context, id string) (*PromptGroup, error) {
	return dbutil.QueryRow[PromptGroup](ctx, r.db,
		`SELECT id, name, description, category, command, user_id, author_name,
			production_prompt_id, is_shared, is_pinned, created_at, updated_at
		 FROM prompt_groups WHERE id = ?`, id)
}

func (r *PromptGroupRepo) ListByUser(ctx context.Context, userID string, sharedIDs []string,
	category, search string, limit, offset int) ([]*PromptGroup, int, error) {

	var conditions []string
	var args []any

	if len(sharedIDs) > 0 {
		placeholders := make([]string, len(sharedIDs))
		for i, id := range sharedIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conditions = append(conditions, fmt.Sprintf("(user_id = ? OR id IN (%s))", strings.Join(placeholders, ",")))
		args = append([]any{userID}, args...)
	} else {
		conditions = append(conditions, "user_id = ?")
		args = append(args, userID)
	}

	if category != "" {
		conditions = append(conditions, "category = ?")
		args = append(args, category)
	}
	if search != "" {
		namePred, like := dbutil.LikeContainsFold("name", search)
		descPred, _ := dbutil.LikeContainsFold("description", search)
		conditions = append(conditions, "("+namePred+" OR "+descPred+")")
		args = append(args, like, like)
	}

	where := strings.Join(conditions, " AND ")

	total, err := dbutil.QueryVal[int](ctx, r.db, "SELECT COUNT(*) FROM prompt_groups WHERE "+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("PromptGroupRepo.ListByUser count: %w", err)
	}

	query := "SELECT id, name, description, category, command, user_id, author_name, " +
		"production_prompt_id, is_shared, is_pinned, created_at, updated_at " +
		"FROM prompt_groups WHERE " + where +
		" ORDER BY is_pinned DESC, created_at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	groups, err := dbutil.QueryRowPtrs[PromptGroup](ctx, r.db, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("PromptGroupRepo.ListByUser query: %w", err)
	}
	return groups, total, nil
}

func (r *PromptGroupRepo) ListAllByUser(ctx context.Context, userID string, sharedIDs []string) ([]*PromptGroup, error) {
	var query string
	var args []any

	if len(sharedIDs) > 0 {
		placeholders := make([]string, len(sharedIDs))
		for i, id := range sharedIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query = fmt.Sprintf(
			`SELECT id, name, description, category, command, user_id, author_name,
				production_prompt_id, is_shared, is_pinned, created_at, updated_at
			 FROM prompt_groups WHERE user_id = ? OR id IN (%s)
			 ORDER BY is_pinned DESC, created_at DESC`,
			strings.Join(placeholders, ","))
		args = append([]any{userID}, args...)
	} else {
		query = `SELECT id, name, description, category, command, user_id, author_name,
				production_prompt_id, is_shared, is_pinned, created_at, updated_at
			 FROM prompt_groups WHERE user_id = ?
			 ORDER BY is_pinned DESC, created_at DESC`
		args = append(args, userID)
	}

	return dbutil.QueryRowPtrs[PromptGroup](ctx, r.db, query, args...)
}

func (r *PromptGroupRepo) Update(ctx context.Context, g *PromptGroup) error {
	g.UpdatedAt = time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE prompt_groups SET name = ?, description = ?, category = ?, command = ?,
			author_name = ?, production_prompt_id = ?, is_shared = ?, is_pinned = ?, updated_at = ?
		 WHERE id = ?`,
		g.Name, g.Description, g.Category, g.Command,
		g.AuthorName, g.ProductionPromptID, dbutil.BoolToInt(g.IsShared), dbutil.BoolToInt(g.IsPinned), g.UpdatedAt, g.ID,
	)
	if err != nil {
		return fmt.Errorf("PromptGroupRepo.Update(%s): %w", g.ID, err)
	}
	return nil
}

func (r *PromptGroupRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM prompt_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("PromptGroupRepo.Delete(%s): %w", id, err)
	}
	return dbutil.CheckRowsAffected(result)
}

func (r *PromptGroupRepo) SetProductionPrompt(ctx context.Context, groupID, promptID string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE prompt_groups SET production_prompt_id = ?, updated_at = ? WHERE id = ?`,
		promptID, now, groupID,
	)
	if err != nil {
		return fmt.Errorf("PromptGroupRepo.SetProductionPrompt(%s): %w", groupID, err)
	}
	return nil
}

func (r *PromptGroupRepo) CommandExistsForUser(ctx context.Context, command, userID string, excludeGroupID string) (bool, error) {
	query := `SELECT COUNT(*) FROM prompt_groups WHERE command = ? AND user_id = ?`
	args := []any{command, userID}
	if excludeGroupID != "" {
		query += " AND id != ?"
		args = append(args, excludeGroupID)
	}
	count, err := dbutil.QueryVal[int](ctx, r.db, query, args...)
	if err != nil {
		return false, fmt.Errorf("PromptGroupRepo.CommandExistsForUser(%s): %w", command, err)
	}
	return count > 0, nil
}
