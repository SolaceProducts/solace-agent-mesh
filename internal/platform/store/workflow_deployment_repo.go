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

type WorkflowDeploymentRepo struct {
	db *DB
}

func NewWorkflowDeploymentRepo(db *DB) *WorkflowDeploymentRepo {
	return &WorkflowDeploymentRepo{db: db}
}

func (r *WorkflowDeploymentRepo) Create(ctx context.Context, d *WorkflowDeployment) error {
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
		`INSERT INTO workflow_deployments (id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id, created_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.WorkflowID, d.Status, d.Action, d.WorkflowConfigSnapshot, d.ErrorMessage, d.FailureReason, d.ErrorID,
		d.CreatedBy, d.CreatedTime, d.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("insert workflow deployment: %w", err)
	}
	return nil
}

func (r *WorkflowDeploymentRepo) GetByID(ctx context.Context, id string) (*WorkflowDeployment, error) {
	return dbutil.QueryRow[WorkflowDeployment](ctx, r.db,
		`SELECT id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id,
			created_by, created_time, updated_time
		 FROM workflow_deployments WHERE id = ?`, id,
	)
}

func (r *WorkflowDeploymentRepo) GetLatestByWorkflowID(ctx context.Context, workflowID string) (*WorkflowDeployment, error) {
	return dbutil.QueryRow[WorkflowDeployment](ctx, r.db,
		`SELECT id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id,
			created_by, created_time, updated_time
		 FROM workflow_deployments WHERE workflow_id = ?
		 ORDER BY created_time DESC, id DESC LIMIT 1`, workflowID,
	)
}

func (r *WorkflowDeploymentRepo) GetLatestSuccessfulDeploy(ctx context.Context, workflowID string) (*WorkflowDeployment, error) {
	return dbutil.QueryRow[WorkflowDeployment](ctx, r.db,
		`SELECT id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id,
			created_by, created_time, updated_time
		 FROM workflow_deployments
		 WHERE workflow_id = ? AND status = 'success' AND action IN ('deploy', 'update')
		 ORDER BY created_time DESC, id DESC LIMIT 1`, workflowID,
	)
}

func (r *WorkflowDeploymentRepo) GetLatestSuccessfulUndeploy(ctx context.Context, workflowID string) (*WorkflowDeployment, error) {
	return dbutil.QueryRow[WorkflowDeployment](ctx, r.db,
		`SELECT id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id,
			created_by, created_time, updated_time
		 FROM workflow_deployments
		 WHERE workflow_id = ? AND status = 'success' AND action = 'undeploy'
		 ORDER BY created_time DESC, id DESC LIMIT 1`, workflowID,
	)
}

func (r *WorkflowDeploymentRepo) ListLiveDeploys(ctx context.Context) ([]WorkflowDeployment, error) {
	deployments, err := dbutil.QueryRows[WorkflowDeployment](ctx, r.db,
		`SELECT d.id, d.workflow_id, d.status, d.action, d.workflow_config_snapshot, d.error_message, d.failure_reason, d.error_id,
			d.created_by, d.created_time, d.updated_time
		 FROM workflow_deployments d
		 WHERE d.status = 'success' AND d.action IN ('deploy', 'update')
		   AND NOT EXISTS (
		     SELECT 1 FROM workflow_deployments n
		     WHERE n.workflow_id = d.workflow_id
		       AND n.status = 'success' AND n.action IN ('deploy', 'update', 'undeploy')
		       AND (n.created_time > d.created_time OR (n.created_time = d.created_time AND n.id > d.id))
		   )`,
	)
	if err != nil {
		return nil, fmt.Errorf("WorkflowDeploymentRepo.ListLiveDeploys: %w", err)
	}
	return deployments, nil
}

func (r *WorkflowDeploymentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]WorkflowDeployment, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM workflow_deployments`)
	if err != nil {
		return nil, 0, fmt.Errorf("WorkflowDeploymentRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	deployments, err := dbutil.QueryRows[WorkflowDeployment](ctx, r.db,
		`SELECT id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id,
			created_by, created_time, updated_time
		 FROM workflow_deployments ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("WorkflowDeploymentRepo.ListPaginated query: %w", err)
	}

	return deployments, total, nil
}
