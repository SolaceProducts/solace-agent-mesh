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

type Inventory struct {
	Agents           int
	Workflows        int
	Connectors       int
	Gateways         int
	Toolsets         int
	ConfiguredModels int
	ConnectorTypes   []string
	GatewayTypes     []string

	AgentsWithCustomToolsets    int
	WorkflowsWithCustomToolsets int

	BuiltinAgents         int
	DeployedBuiltinAgents int

	DeployedAgents                      int
	DeployedWorkflows                   int
	DeployedAgentsWithCustomToolsets    int
	DeployedWorkflowsWithCustomToolsets int
}

type InventoryRepo struct {
	db *DB
}

func NewInventoryRepo(db *DB) *InventoryRepo {
	return &InventoryRepo{db: db}
}

const SeededAgentCreator = "system"

const countableAgents = `agents WHERE type NOT IN ('a2a_proxy', 'link') AND is_ephemeral = 0 AND created_by != ?`

const seededAgents = `agents WHERE type NOT IN ('a2a_proxy', 'link') AND is_ephemeral = 0 AND created_by = ?`

const authoredAgentToolsetJoin = `a.type NOT IN ('a2a_proxy', 'link') AND a.is_ephemeral = 0 AND a.created_by != ?`

func (r *InventoryRepo) Snapshot(ctx context.Context) (Inventory, error) {
	var inv Inventory
	var err error

	if inv.Agents, err = r.CountAgents(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.Workflows, err = r.CountWorkflows(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.Connectors, err = r.CountConnectors(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.Gateways, err = r.CountGateways(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.ConfiguredModels, err = r.CountConfiguredModels(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.Toolsets, err = r.CountToolsets(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.AgentsWithCustomToolsets, err = r.CountAgentsWithCustomToolsets(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.WorkflowsWithCustomToolsets, err = r.CountWorkflowsWithCustomToolsets(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.BuiltinAgents, err = r.CountBuiltinAgents(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.DeployedAgents, err = r.CountDeployedAgents(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.DeployedBuiltinAgents, err = r.CountDeployedBuiltinAgents(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.DeployedWorkflows, err = r.CountDeployedWorkflows(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.DeployedAgentsWithCustomToolsets, err = r.CountDeployedAgentsWithCustomToolsets(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.DeployedWorkflowsWithCustomToolsets, err = r.CountDeployedWorkflowsWithCustomToolsets(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.ConnectorTypes, err = r.DistinctConnectorTypes(ctx); err != nil {
		return Inventory{}, err
	}
	if inv.GatewayTypes, err = r.DistinctGatewayTypes(ctx); err != nil {
		return Inventory{}, err
	}
	return inv, nil
}

func (r *InventoryRepo) CountAgents(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM `+countableAgents, SeededAgentCreator)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountAgents: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountBuiltinAgents(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM `+seededAgents, SeededAgentCreator)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountBuiltinAgents: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountWorkflows(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM workflows`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountWorkflows: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountConnectors(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM connectors`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountConnectors: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountGateways(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM gateways`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountGateways: %w", err)
	}
	return n, nil
}

const ModelConfigPlaceholder = "undefined"

func (r *InventoryRepo) CountConfiguredModels(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM model_configurations WHERE model_name IS NOT NULL AND model_name != '' AND model_name != ?`, ModelConfigPlaceholder)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountConfiguredModels: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountToolsets(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM tool_packages`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountToolsets: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountAgentsWithCustomToolsets(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(DISTINCT at.agent_id)
		 FROM agent_toolsets at
		 JOIN tool_packages tp ON tp.name = at.toolset
		 JOIN agents a ON a.id = at.agent_id
		 WHERE `+authoredAgentToolsetJoin, SeededAgentCreator)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountAgentsWithCustomToolsets: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountWorkflowsWithCustomToolsets(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(DISTINCT wt.workflow_id)
		 FROM workflow_toolsets wt
		 JOIN tool_packages tp ON tp.name = wt.toolset`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountWorkflowsWithCustomToolsets: %w", err)
	}
	return n, nil
}

const (
	deployedAgentIDs = `SELECT resource_id FROM (
		    SELECT agent_id AS resource_id, status, action,
		           ROW_NUMBER() OVER (PARTITION BY agent_id ORDER BY created_time DESC, id DESC) AS rn
		    FROM agent_deployments
		) latest
		WHERE rn = 1 AND status = 'success' AND action IN ('deploy', 'update')`

	deployedWorkflowIDs = `SELECT resource_id FROM (
		    SELECT workflow_id AS resource_id, status, action,
		           ROW_NUMBER() OVER (PARTITION BY workflow_id ORDER BY created_time DESC, id DESC) AS rn
		    FROM workflow_deployments
		) latest
		WHERE rn = 1 AND status = 'success' AND action IN ('deploy', 'update')`
)

func (r *InventoryRepo) CountDeployedAgents(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM `+countableAgents+` AND id IN (`+deployedAgentIDs+`)`, SeededAgentCreator)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountDeployedAgents: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountDeployedBuiltinAgents(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM `+seededAgents+` AND id IN (`+deployedAgentIDs+`)`, SeededAgentCreator)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountDeployedBuiltinAgents: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountDeployedWorkflows(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM workflows WHERE id IN (`+deployedWorkflowIDs+`)`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountDeployedWorkflows: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountDeployedAgentsWithCustomToolsets(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(DISTINCT at.agent_id)
		 FROM agent_toolsets at
		 JOIN tool_packages tp ON tp.name = at.toolset
		 JOIN agents a ON a.id = at.agent_id
		 WHERE `+authoredAgentToolsetJoin+`
		   AND a.id IN (`+deployedAgentIDs+`)`, SeededAgentCreator)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountDeployedAgentsWithCustomToolsets: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) CountDeployedWorkflowsWithCustomToolsets(ctx context.Context) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(DISTINCT wt.workflow_id)
		 FROM workflow_toolsets wt
		 JOIN tool_packages tp ON tp.name = wt.toolset
		 WHERE wt.workflow_id IN (`+deployedWorkflowIDs+`)`)
	if err != nil {
		return 0, fmt.Errorf("InventoryRepo.CountDeployedWorkflowsWithCustomToolsets: %w", err)
	}
	return n, nil
}

func (r *InventoryRepo) DistinctConnectorTypes(ctx context.Context) ([]string, error) {
	types, err := dbutil.QueryVals[string](ctx, r.db, `SELECT DISTINCT type FROM connectors`)
	if err != nil {
		return nil, fmt.Errorf("InventoryRepo.DistinctConnectorTypes: %w", err)
	}
	return types, nil
}

func (r *InventoryRepo) DistinctGatewayTypes(ctx context.Context) ([]string, error) {
	types, err := dbutil.QueryVals[string](ctx, r.db, `SELECT DISTINCT type FROM gateways`)
	if err != nil {
		return nil, fmt.Errorf("InventoryRepo.DistinctGatewayTypes: %w", err)
	}
	return types, nil
}
