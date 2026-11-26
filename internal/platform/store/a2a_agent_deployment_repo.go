// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type A2AAgentDeploymentRepo struct {
	db *DB
}

func NewA2AAgentDeploymentRepo(db *DB) *A2AAgentDeploymentRepo {
	return &A2AAgentDeploymentRepo{db: db}
}

func (r *A2AAgentDeploymentRepo) Create(ctx context.Context, d *A2AAgentDeployment) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO a2a_agent_deployments (a2a_agent_id, agent_deployment_id, action, config_snapshot)
		 VALUES (?, ?, ?, ?)`,
		d.A2AAgentID, d.AgentDeploymentID, d.Action, d.ConfigSnapshot,
	)
	if err != nil {
		return fmt.Errorf("A2AAgentDeploymentRepo.Create(%s): %w", d.A2AAgentID, err)
	}
	return nil
}

func (r *A2AAgentDeploymentRepo) GetByDeploymentID(ctx context.Context, deploymentID string) (*A2AAgentDeployment, error) {
	return dbutil.QueryRow[A2AAgentDeployment](ctx, r.db,
		`SELECT a2a_agent_id, agent_deployment_id, action, config_snapshot
		 FROM a2a_agent_deployments WHERE agent_deployment_id = ?`, deploymentID,
	)
}

var joinedAgentDeploymentCols = qualifiedColumns("d", agentDeploymentCols)

func qualifiedColumns(alias, columns string) string {
	names := strings.Split(columns, ",")
	for i, name := range names {
		names[i] = alias + "." + strings.TrimSpace(name)
	}
	return strings.Join(names, ", ")
}

func (r *A2AAgentDeploymentRepo) ListByA2AAgentID(ctx context.Context, a2aAgentID string, pageNumber, pageSize int) ([]*AgentDeployment, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM a2a_agent_deployments WHERE a2a_agent_id = ?`, a2aAgentID)
	if err != nil {
		return nil, 0, fmt.Errorf("A2AAgentDeploymentRepo.ListByA2AAgentID(%s) count: %w", a2aAgentID, err)
	}

	offset := (pageNumber - 1) * pageSize
	deployments, err := dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db,
		`SELECT `+joinedAgentDeploymentCols+`
		 FROM agent_deployments d
		 JOIN a2a_agent_deployments ad ON d.id = ad.agent_deployment_id
		 WHERE ad.a2a_agent_id = ?
		 ORDER BY d.created_time DESC, d.id DESC LIMIT ? OFFSET ?`,
		a2aAgentID, pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("A2AAgentDeploymentRepo.ListByA2AAgentID(%s) query: %w", a2aAgentID, err)
	}

	return deployments, total, nil
}

func (r *A2AAgentDeploymentRepo) ListAllPaginated(ctx context.Context, pageNumber, pageSize int) ([]*AgentDeployment, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM a2a_agent_deployments`)
	if err != nil {
		return nil, 0, fmt.Errorf("A2AAgentDeploymentRepo.ListAllPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	deployments, err := dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db,
		`SELECT `+joinedAgentDeploymentCols+`
		 FROM agent_deployments d
		 JOIN a2a_agent_deployments ad ON d.id = ad.agent_deployment_id
		 ORDER BY d.created_time DESC, d.id DESC LIMIT ? OFFSET ?`,
		pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("A2AAgentDeploymentRepo.ListAllPaginated query: %w", err)
	}

	return deployments, total, nil
}
