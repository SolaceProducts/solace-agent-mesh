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

const desiredRowFilter = `(status = 'success' OR (failure_reason = 'runtime_error' AND (action = 'undeploy' OR %[1]s IS NOT NULL)))`

const newestFirst = `created_time DESC, id DESC`

func desiredRowsQuery(cols, table, objectCol, snapshotCol string) string {
	return fmt.Sprintf(`SELECT %[1]s FROM (
		  SELECT %[1]s, ROW_NUMBER() OVER (PARTITION BY %[3]s ORDER BY `+newestFirst+`) AS newest
		  FROM %[2]s WHERE `+fmt.Sprintf(desiredRowFilter, snapshotCol)+`
		) ranked WHERE newest = 1`, cols, table, objectCol)
}

func desiredRowQuery(cols, table, objectCol, snapshotCol string) string {
	return fmt.Sprintf(`SELECT %s FROM %s WHERE %s = ? AND `+fmt.Sprintf(desiredRowFilter, snapshotCol)+` ORDER BY `+newestFirst+` LIMIT 1`, cols, table, objectCol)
}

const workflowDeploymentCols = `id, workflow_id, status, action, workflow_config_snapshot, error_message, failure_reason, error_id, created_by, created_time, updated_time`

func (r *GatewayDeploymentRepo) Desired(ctx context.Context) (map[string]*GatewayDeployment, error) {
	rows, err := dbutil.QueryRowPtrs[GatewayDeployment](ctx, r.db,
		desiredRowsQuery(gwDeploymentCols, "gateway_deployments", "gateway_id", "gateway_config_snapshot"))
	if err != nil {
		return nil, fmt.Errorf("GatewayDeploymentRepo.Desired: %w", err)
	}
	out := make(map[string]*GatewayDeployment, len(rows))
	for _, row := range rows {
		out[row.GatewayID] = row
	}
	return out, nil
}

func (r *AgentDeploymentRepo) Desired(ctx context.Context) (map[string]*AgentDeployment, error) {
	rows, err := dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db,
		desiredRowsQuery(agentDeploymentCols, "agent_deployments", "agent_id", "agent_config_snapshot"))
	if err != nil {
		return nil, fmt.Errorf("AgentDeploymentRepo.Desired: %w", err)
	}
	out := make(map[string]*AgentDeployment, len(rows))
	for _, row := range rows {
		out[row.AgentID] = row
	}
	return out, nil
}

func (r *WorkflowDeploymentRepo) Desired(ctx context.Context) (map[string]*WorkflowDeployment, error) {
	rows, err := dbutil.QueryRowPtrs[WorkflowDeployment](ctx, r.db,
		desiredRowsQuery(workflowDeploymentCols, "workflow_deployments", "workflow_id", "workflow_config_snapshot"))
	if err != nil {
		return nil, fmt.Errorf("WorkflowDeploymentRepo.Desired: %w", err)
	}
	out := make(map[string]*WorkflowDeployment, len(rows))
	for _, row := range rows {
		out[row.WorkflowID] = row
	}
	return out, nil
}

func (r *AgentDeploymentRepo) DesiredFor(ctx context.Context, agentID string) (*AgentDeployment, error) {
	row, err := dbutil.QueryRow[AgentDeployment](ctx, r.db,
		desiredRowQuery(agentDeploymentCols, "agent_deployments", "agent_id", "agent_config_snapshot"), agentID)
	if err != nil {
		return nil, fmt.Errorf("AgentDeploymentRepo.DesiredFor(%s): %w", agentID, err)
	}
	return row, nil
}

func (r *WorkflowDeploymentRepo) DesiredFor(ctx context.Context, workflowID string) (*WorkflowDeployment, error) {
	row, err := dbutil.QueryRow[WorkflowDeployment](ctx, r.db,
		desiredRowQuery(workflowDeploymentCols, "workflow_deployments", "workflow_id", "workflow_config_snapshot"), workflowID)
	if err != nil {
		return nil, fmt.Errorf("WorkflowDeploymentRepo.DesiredFor(%s): %w", workflowID, err)
	}
	return row, nil
}

func (r *GatewayDeploymentRepo) DesiredFor(ctx context.Context, gatewayID string) (*GatewayDeployment, error) {
	row, err := dbutil.QueryRow[GatewayDeployment](ctx, r.db,
		desiredRowQuery(gwDeploymentCols, "gateway_deployments", "gateway_id", "gateway_config_snapshot"), gatewayID)
	if err != nil {
		return nil, fmt.Errorf("GatewayDeploymentRepo.DesiredFor(%s): %w", gatewayID, err)
	}
	return row, nil
}
