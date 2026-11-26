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
)

type EntrypointTaskItemRepo struct {
	db *DB
}

func NewEntrypointTaskItemRepo(db *DB) *EntrypointTaskItemRepo {
	return &EntrypointTaskItemRepo{db: db}
}

type entrypointTaskItemRow struct {
	Item  string `db:"item"`
	Value string `db:"value"`
}

func (r *EntrypointTaskItemRepo) Put(ctx context.Context, taskID, item, value string) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO entrypoint_task_items (task_id, item, value, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (task_id, item) DO UPDATE SET value = excluded.value`,
		taskID, item, value, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("EntrypointTaskItemRepo.Put(%s, %s): %w", taskID, itemKindOf(item), err)
	}
	return nil
}

func (r *EntrypointTaskItemRepo) Claim(ctx context.Context, taskID, item, value string) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO entrypoint_task_items (task_id, item, value, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (task_id, item) DO NOTHING`,
		taskID, item, value, time.Now().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("EntrypointTaskItemRepo.Claim(%s, %s): %w", taskID, itemKindOf(item), err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointTaskItemRepo.Claim(%s, %s) rows affected: %w", taskID, itemKindOf(item), err)
	}
	return n == 1, nil
}

func (r *EntrypointTaskItemRepo) Replace(ctx context.Context, taskID, item, old, value string) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_task_items SET value = ? WHERE task_id = ? AND item = ? AND value = ?`,
		value, taskID, item, old)
	if err != nil {
		return false, fmt.Errorf("EntrypointTaskItemRepo.Replace(%s, %s): %w", taskID, itemKindOf(item), err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointTaskItemRepo.Replace(%s, %s) rows affected: %w", taskID, itemKindOf(item), err)
	}
	return n == 1, nil
}

func (r *EntrypointTaskItemRepo) Get(ctx context.Context, taskID, item string) (string, bool, error) {
	row, err := dbutil.QueryRow[entrypointTaskItemRow](ctx, r.db,
		`SELECT item, value FROM entrypoint_task_items WHERE task_id = ? AND item = ?`, taskID, item)
	if err != nil {
		return "", false, fmt.Errorf("EntrypointTaskItemRepo.Get(%s, %s): %w", taskID, itemKindOf(item), err)
	}
	if row == nil {
		return "", false, nil
	}
	return row.Value, true, nil
}

func (r *EntrypointTaskItemRepo) List(ctx context.Context, taskID string) (map[string]string, error) {
	rows, err := dbutil.QueryRows[entrypointTaskItemRow](ctx, r.db,
		`SELECT item, value FROM entrypoint_task_items WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("EntrypointTaskItemRepo.List(%s): %w", taskID, err)
	}
	items := make(map[string]string, len(rows))
	for _, row := range rows {
		items[row.Item] = row.Value
	}
	return items, nil
}

func (r *EntrypointTaskItemRepo) DeleteIf(ctx context.Context, taskID, item, value string) (bool, error) {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM entrypoint_task_items WHERE task_id = ? AND item = ? AND value = ?`, taskID, item, value)
	if err != nil {
		return false, fmt.Errorf("EntrypointTaskItemRepo.DeleteIf(%s, %s): %w", taskID, itemKindOf(item), err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointTaskItemRepo.DeleteIf(%s, %s) rows affected: %w", taskID, itemKindOf(item), err)
	}
	return n == 1, nil
}

func (r *EntrypointTaskItemRepo) Delete(ctx context.Context, taskID, item string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM entrypoint_task_items WHERE task_id = ? AND item = ?`, taskID, item); err != nil {
		return fmt.Errorf("EntrypointTaskItemRepo.Delete(%s, %s): %w", taskID, itemKindOf(item), err)
	}
	return nil
}

func itemKindOf(item string) string {
	kind, _, _ := strings.Cut(item, ":")
	return kind
}
