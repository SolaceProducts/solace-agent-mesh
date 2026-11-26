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

func (r *AgentRepo) NamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	return namesByIDs(ctx, r.db, "agents", ids)
}

func (r *WorkflowRepo) NamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	return namesByIDs(ctx, r.db, "workflows", ids)
}

func namesByIDs(ctx context.Context, db *DB, table string, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("%s NamesByIDs: %w", table, err)
	}
	type row struct {
		ID   string `db:"id"`
		Name string `db:"name"`
	}
	rows, err := dbutil.QueryRows[row](ctx, db, `SELECT id, name FROM `+table+` WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("%s NamesByIDs: %w", table, err)
	}
	for _, r := range rows {
		result[r.ID] = r.Name
	}
	return result, nil
}
