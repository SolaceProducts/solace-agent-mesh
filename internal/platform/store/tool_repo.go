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

type ToolRepo struct {
	db *DB
}

func NewToolRepo(db *DB) *ToolRepo {
	return &ToolRepo{db: db}
}

func (r *ToolRepo) Create(ctx context.Context, t *Tool) error {
	if t.ID == "" {
		t.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if t.CreatedTime == 0 {
		t.CreatedTime = now
	}
	if t.UpdatedTime == 0 {
		t.UpdatedTime = now
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tools (id, name, description, origin, status,
			parameters, instructions, artifact_params,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Description, t.Origin, t.Status,
		t.Parameters, t.Instructions, t.ArtifactParams,
		t.CreatedBy, t.UpdatedBy, t.CreatedTime, t.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("insert tool: %w", err)
	}
	return nil
}

func (r *ToolRepo) GetByID(ctx context.Context, id string) (*Tool, error) {
	return dbutil.QueryRow[Tool](ctx, r.db,
		`SELECT id, name, description, origin, status,
			parameters, instructions, artifact_params,
			created_by, updated_by, created_time, updated_time
		 FROM tools WHERE id = ?`, id,
	)
}

func (r *ToolRepo) GetByName(ctx context.Context, name string) (*Tool, error) {
	return dbutil.QueryRow[Tool](ctx, r.db,
		`SELECT id, name, description, origin, status,
			parameters, instructions, artifact_params,
			created_by, updated_by, created_time, updated_time
		 FROM tools WHERE name = ?`, name,
	)
}

func (r *ToolRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]Tool, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM tools`)
	if err != nil {
		return nil, 0, fmt.Errorf("ToolRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	tools, err := dbutil.QueryRows[Tool](ctx, r.db,
		`SELECT id, name, description, origin, status,
			parameters, instructions, artifact_params,
			created_by, updated_by, created_time, updated_time
		 FROM tools ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("ToolRepo.ListPaginated query: %w", err)
	}

	return tools, total, nil
}

func (r *ToolRepo) UpsertByName(ctx context.Context, t *Tool) error {
	if t.ID == "" {
		t.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if t.CreatedTime == 0 {
		t.CreatedTime = now
	}

	insertSQL := `INSERT INTO tools (id, name, description, status, parameters, instructions, artifact_params, origin, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "name",
		[]string{"description", "status", "parameters", "instructions", "artifact_params", "updated_by", "updated_time"})

	_, err := r.db.ExecContext(ctx, upsertSQL,
		t.ID, t.Name, t.Description, t.Status, t.Parameters, t.Instructions, t.ArtifactParams,
		t.Origin, t.CreatedBy, t.UpdatedBy, t.CreatedTime, now)
	if err != nil {
		return fmt.Errorf("ToolRepo.UpsertByName(%s): %w", t.Name, err)
	}
	return nil
}

func (r *ToolRepo) Update(ctx context.Context, t *Tool) error {
	t.UpdatedTime = time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE tools SET name = ?, description = ?, origin = ?, status = ?,
			parameters = ?, instructions = ?, artifact_params = ?,
			updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		t.Name, t.Description, t.Origin, t.Status,
		t.Parameters, t.Instructions, t.ArtifactParams,
		t.UpdatedBy, t.UpdatedTime, t.ID,
	)
	if err != nil {
		return fmt.Errorf("update tool: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool not found")
	}
	return nil
}

func (r *ToolRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM tools WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("ToolRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tool not found")
	}
	return nil
}

func (r *ToolRepo) SetStatus(ctx context.Context, name, status string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE tools SET status = ?, updated_time = ? WHERE name = ?`,
		status, now, name,
	)
	if err != nil {
		return fmt.Errorf("ToolRepo.SetStatus(%s): %w", name, err)
	}
	return nil
}

func (r *ToolRepo) Touch(ctx context.Context, name string, nowMs int64) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE tools SET updated_time = ? WHERE name = ?`, nowMs, name); err != nil {
		return fmt.Errorf("ToolRepo.Touch(%s): %w", name, err)
	}
	return nil
}
