// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/google/uuid"
)

type DeployHistoryRepo struct {
	db *DB
}

func NewDeployHistoryRepo(db *DB) *DeployHistoryRepo {
	return &DeployHistoryRepo{db: db}
}

const (
	firstAgentDeploy = `SELECT COUNT(*) FROM agent_deployments d
		WHERE d.id = ? AND d.status = 'success' AND d.action IN ('deploy', 'update')
		  AND NOT EXISTS (
		    SELECT 1 FROM agent_deployments prior
		    WHERE prior.agent_id = d.agent_id
		      AND prior.status = 'success' AND prior.action IN ('deploy', 'update')
		      AND (prior.created_time < d.created_time
		           OR (prior.created_time = d.created_time AND prior.id < d.id))
		  )`

	firstWorkflowDeploy = `SELECT COUNT(*) FROM workflow_deployments d
		WHERE d.id = ? AND d.status = 'success' AND d.action IN ('deploy', 'update')
		  AND NOT EXISTS (
		    SELECT 1 FROM workflow_deployments prior
		    WHERE prior.workflow_id = d.workflow_id
		      AND prior.status = 'success' AND prior.action IN ('deploy', 'update')
		      AND (prior.created_time < d.created_time
		           OR (prior.created_time = d.created_time AND prior.id < d.id))
		  )`
)

func (r *DeployHistoryRepo) IsFirstAgentDeploy(ctx context.Context, deploymentID string) (bool, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return false, nil
	}
	n, err := dbutil.QueryVal[int](ctx, r.db, firstAgentDeploy, deploymentID)
	if err != nil {
		return false, fmt.Errorf("DeployHistoryRepo.IsFirstAgentDeploy(%s): %w", deploymentID, err)
	}
	return n == 1, nil
}

func (r *DeployHistoryRepo) IsFirstWorkflowDeploy(ctx context.Context, deploymentID string) (bool, error) {
	if _, err := uuid.Parse(deploymentID); err != nil {
		return false, nil
	}
	n, err := dbutil.QueryVal[int](ctx, r.db, firstWorkflowDeploy, deploymentID)
	if err != nil {
		return false, fmt.Errorf("DeployHistoryRepo.IsFirstWorkflowDeploy(%s): %w", deploymentID, err)
	}
	return n == 1, nil
}
