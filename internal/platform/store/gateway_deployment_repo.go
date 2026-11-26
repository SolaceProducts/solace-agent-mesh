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

const gwDeploymentCols = `id, gateway_id, status, action, gateway_config_snapshot, config_hash, prompt_transform_hash, gateway_data_snapshot, error_message, failure_reason, error_id, created_by, created_time, updated_time`

type GatewayDeploymentRepo struct {
	db *DB
}

func NewGatewayDeploymentRepo(db *DB) *GatewayDeploymentRepo {
	return &GatewayDeploymentRepo{db: db}
}

func (r *GatewayDeploymentRepo) Create(ctx context.Context, d *GatewayDeployment) error {
	if d.ID == "" {
		d.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if d.CreatedTime == 0 {
		d.CreatedTime = now
	}
	if d.UpdatedTime == 0 {
		d.UpdatedTime = now
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO gateway_deployments (`+gwDeploymentCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.GatewayID, d.Status, d.Action, d.GatewayConfigSnapshot, d.ConfigHash, d.PromptTransformHash, d.GatewayDataSnapshot,
		d.ErrorMessage, d.FailureReason, d.ErrorID, d.CreatedBy, d.CreatedTime, d.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("GatewayDeploymentRepo.Create(%s): %w", d.ID, err)
	}
	return nil
}

func (r *GatewayDeploymentRepo) GetByID(ctx context.Context, id string) (*GatewayDeployment, error) {
	return dbutil.QueryRow[GatewayDeployment](ctx, r.db,
		`SELECT `+gwDeploymentCols+` FROM gateway_deployments WHERE id = ?`, id,
	)
}

func (r *GatewayDeploymentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int, gatewayID string) ([]*GatewayDeployment, int, error) {
	where := ""
	var args []any
	if gatewayID != "" {
		where = " WHERE gateway_id = ?"
		args = append(args, gatewayID)
	}

	countQuery := `SELECT COUNT(*) FROM gateway_deployments` + where
	total, err := dbutil.QueryVal[int](ctx, r.db, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("GatewayDeploymentRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	query := `SELECT ` + gwDeploymentCols + ` FROM gateway_deployments` + where + ` ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)

	deployments, err := dbutil.QueryRowPtrs[GatewayDeployment](ctx, r.db, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("GatewayDeploymentRepo.ListPaginated query: %w", err)
	}

	return deployments, total, nil
}

func (r *GatewayDeploymentRepo) UpdateStatus(ctx context.Context, id, status string, errorMessage *string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE gateway_deployments SET status = ?, error_message = ?, updated_time = ? WHERE id = ?`,
		status, errorMessage, now, id,
	)
	if err != nil {
		return fmt.Errorf("update gateway deployment status: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("gateway deployment not found")
	}
	return nil
}

func (r *GatewayDeploymentRepo) GetLatestByGatewayID(ctx context.Context, gatewayID string) (*GatewayDeployment, error) {
	return dbutil.QueryRow[GatewayDeployment](ctx, r.db,
		`SELECT `+gwDeploymentCols+` FROM gateway_deployments WHERE gateway_id = ? ORDER BY created_time DESC, id DESC LIMIT 1`, gatewayID,
	)
}

func (r *GatewayDeploymentRepo) GetLatestSuccessfulDeploys(ctx context.Context, gatewayIDs []string) (map[string]*GatewayDeployment, error) {
	if len(gatewayIDs) == 0 {
		return map[string]*GatewayDeployment{}, nil
	}

	ph, args, err := dbutil.InClause(gatewayIDs)
	if err != nil {
		return nil, fmt.Errorf("GatewayDeploymentRepo.GetLatestSuccessfulDeploys: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT d.id, d.gateway_id, d.status, d.action, d.gateway_config_snapshot, d.config_hash, d.prompt_transform_hash, d.gateway_data_snapshot, d.error_message, d.failure_reason, d.error_id, d.created_by, d.created_time, d.updated_time
		 FROM gateway_deployments d
		 INNER JOIN (
		   SELECT gateway_id, MAX(created_time) as max_time
		   FROM gateway_deployments
		   WHERE gateway_id IN (%s) AND status = 'success' AND action IN ('deploy', 'update')
		   GROUP BY gateway_id
		 ) latest ON d.gateway_id = latest.gateway_id AND d.created_time = latest.max_time
		 WHERE d.status = 'success' AND d.action IN ('deploy', 'update')
		 ORDER BY d.created_time DESC, d.id DESC`,
		ph,
	)

	deployments, err := dbutil.QueryRowPtrs[GatewayDeployment](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("GatewayDeploymentRepo.GetLatestSuccessfulDeploys: %w", err)
	}

	result := make(map[string]*GatewayDeployment, len(deployments))
	for _, d := range deployments {
		if _, seen := result[d.GatewayID]; seen {
			continue
		}
		result[d.GatewayID] = d
	}
	return result, nil
}

func (r *GatewayDeploymentRepo) ListByStatus(ctx context.Context, status string) ([]*GatewayDeployment, error) {
	return dbutil.QueryRowPtrs[GatewayDeployment](ctx, r.db,
		`SELECT `+gwDeploymentCols+` FROM gateway_deployments WHERE status = ? ORDER BY created_time ASC`, status,
	)
}
