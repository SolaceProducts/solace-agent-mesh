// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type CategoryPreferenceRepo struct {
	db *DB
}

func NewCategoryPreferenceRepo(db *DB) *CategoryPreferenceRepo {
	return &CategoryPreferenceRepo{db: db}
}

type categoryRow struct {
	Category string `db:"category"`
}

func (r *CategoryPreferenceRepo) DisabledCategories(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := dbutil.QueryRows[categoryRow](ctx,
		r.db,
		`SELECT category FROM notification_category_preferences WHERE user_id = ? AND enabled = 0`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query disabled categories: %w", err)
	}
	disabled := make(map[string]bool, len(rows))
	for _, row := range rows {
		disabled[row.Category] = true
	}
	return disabled, nil
}

func (r *CategoryPreferenceRepo) SetCategory(ctx context.Context, userID, category string, enabled bool) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notification_category_preferences (user_id, category, enabled, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id, category)
		 DO UPDATE SET enabled = excluded.enabled, updated_at = excluded.updated_at`,
		userID, category, dbutil.BoolToInt(enabled), nowMillis(),
	)
	if err != nil {
		return fmt.Errorf("set category preference: %w", err)
	}
	return nil
}
