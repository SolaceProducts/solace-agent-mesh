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

type DeploymentRepo struct {
	db *DB
}

func NewDeploymentRepo(db *DB) *DeploymentRepo {
	return &DeploymentRepo{db: db}
}

func (r *DeploymentRepo) Create(ctx context.Context, d *AgentDeployment) error {
	if err := insertAgentDeployment(ctx, r.db, d); err != nil {
		return fmt.Errorf("insert deployment: %w", err)
	}
	return nil
}

func (r *DeploymentRepo) GetByID(ctx context.Context, id string) (*AgentDeployment, error) {
	return dbutil.QueryRow[AgentDeployment](ctx, r.db,
		`SELECT `+agentDeploymentCols+` FROM agent_deployments WHERE id = ?`, id,
	)
}

func (r *DeploymentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]AgentDeployment, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM agent_deployments`)
	if err != nil {
		return nil, 0, fmt.Errorf("DeploymentRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	deployments, err := dbutil.QueryRows[AgentDeployment](ctx, r.db,
		`SELECT `+agentDeploymentCols+` FROM agent_deployments ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("DeploymentRepo.ListPaginated query: %w", err)
	}

	return deployments, total, nil
}
