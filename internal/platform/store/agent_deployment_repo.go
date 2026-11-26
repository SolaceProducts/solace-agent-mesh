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

const agentDeploymentCols = `id, agent_id, status, action, agent_config_snapshot, config_hash, ` + agentSurfaceHashCols + `, agent_data_snapshot, deployed_payloads, error_message, failure_reason, error_id, created_by, created_time, updated_time`

const agentSurfaceHashCols = `prompt_hash, toolset_hash, capability_hash, behaviour_hash, card_hash`

func insertAgentDeployment(ctx context.Context, db *DB, d *AgentDeployment) error {
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
	payloads := d.DeployedPayloads
	if !isDesiredAgentDeployment(d) {
		payloads = nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO agent_deployments (`+agentDeploymentCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.AgentID, d.Status, d.Action, d.AgentConfigSnapshot, d.ConfigHash,
		d.PromptHash, d.ToolsetHash, d.CapabilityHash, d.BehaviourHash, d.CardHash,
		d.AgentDataSnapshot, payloads, d.ErrorMessage, d.FailureReason, d.ErrorID, d.CreatedBy, d.CreatedTime, d.UpdatedTime,
	)
	return err
}

func (r *AgentDeploymentRepo) ClearSupersededPayloads(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE agent_deployments SET deployed_payloads = NULL
		 WHERE deployed_payloads IS NOT NULL AND id NOT IN (`+desiredRowsQuery("id", "agent_deployments", "agent_id", "agent_config_snapshot")+`)`)
	if err != nil {
		return 0, fmt.Errorf("AgentDeploymentRepo.ClearSupersededPayloads: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func isDesiredAgentDeployment(d *AgentDeployment) bool {
	if d.Status == "success" {
		return true
	}
	return d.FailureReason != nil && *d.FailureReason == "runtime_error" && (d.Action == "undeploy" || d.AgentConfigSnapshot != nil)
}

type AgentDeploymentRepo struct {
	db *DB
}

func NewAgentDeploymentRepo(db *DB) *AgentDeploymentRepo {
	return &AgentDeploymentRepo{db: db}
}

func (r *AgentDeploymentRepo) Create(ctx context.Context, d *AgentDeployment) error {
	if err := insertAgentDeployment(ctx, r.db, d); err != nil {
		return fmt.Errorf("AgentDeploymentRepo.Create(%s): %w", d.ID, err)
	}
	return nil
}

func (r *AgentDeploymentRepo) GetByID(ctx context.Context, id string) (*AgentDeployment, error) {
	return dbutil.QueryRow[AgentDeployment](ctx, r.db,
		`SELECT `+agentDeploymentCols+` FROM agent_deployments WHERE id = ?`, id,
	)
}

func (r *AgentDeploymentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int, agentID string) ([]*AgentDeployment, int, error) {
	where := ""
	var args []any
	if agentID != "" {
		where = " WHERE agent_id = ?"
		args = append(args, agentID)
	}

	countQuery := `SELECT COUNT(*) FROM agent_deployments` + where
	total, err := dbutil.QueryVal[int](ctx, r.db, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("AgentDeploymentRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	query := `SELECT ` + agentDeploymentCols + ` FROM agent_deployments` + where + ` ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, pageSize, offset)

	deployments, err := dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("AgentDeploymentRepo.ListPaginated query: %w", err)
	}

	return deployments, total, nil
}

func (r *AgentDeploymentRepo) UpdateStatus(ctx context.Context, id, status string, errorMessage *string) error {
	now := time.Now().UnixMilli()
	result, err := r.db.ExecContext(ctx,
		`UPDATE agent_deployments SET status = ?, error_message = ?, updated_time = ? WHERE id = ?`,
		status, errorMessage, now, id,
	)
	if err != nil {
		return fmt.Errorf("update agent deployment status: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent deployment not found")
	}
	return nil
}

func (r *AgentDeploymentRepo) GetLatestByAgentID(ctx context.Context, agentID string) (*AgentDeployment, error) {
	return dbutil.QueryRow[AgentDeployment](ctx, r.db,
		`SELECT `+agentDeploymentCols+` FROM agent_deployments WHERE agent_id = ? ORDER BY created_time DESC, id DESC LIMIT 1`, agentID,
	)
}

func (r *AgentDeploymentRepo) GetLatestSuccessfulDeploys(ctx context.Context, agentIDs []string) (map[string]*AgentDeployment, error) {
	if len(agentIDs) == 0 {
		return map[string]*AgentDeployment{}, nil
	}

	ph, args, err := dbutil.InClause(agentIDs)
	if err != nil {
		return nil, fmt.Errorf("AgentDeploymentRepo.GetLatestSuccessfulDeploys: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT d.id, d.agent_id, d.status, d.action, d.agent_config_snapshot, d.config_hash,
		 d.prompt_hash, d.toolset_hash, d.capability_hash, d.behaviour_hash, d.card_hash,
		 d.agent_data_snapshot, d.deployed_payloads, d.error_message, d.failure_reason, d.error_id, d.created_by, d.created_time, d.updated_time
		 FROM agent_deployments d
		 INNER JOIN (
		   SELECT agent_id, MAX(created_time) as max_time
		   FROM agent_deployments
		   WHERE agent_id IN (%s) AND status = 'success' AND action IN ('deploy', 'update')
		   GROUP BY agent_id
		 ) latest ON d.agent_id = latest.agent_id AND d.created_time = latest.max_time
		 WHERE d.status = 'success' AND d.action IN ('deploy', 'update')
		 ORDER BY d.created_time DESC, d.id DESC`,
		ph,
	)

	deployments, err := dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("AgentDeploymentRepo.GetLatestSuccessfulDeploys: %w", err)
	}

	result := make(map[string]*AgentDeployment, len(deployments))
	for _, d := range deployments {
		if _, seen := result[d.AgentID]; seen {
			continue
		}
		result[d.AgentID] = d
	}
	return result, nil
}

func (r *AgentDeploymentRepo) GetLatestSuccessfulUndeploy(ctx context.Context, agentID string) (*AgentDeployment, error) {
	return dbutil.QueryRow[AgentDeployment](ctx, r.db,
		`SELECT `+agentDeploymentCols+`
		 FROM agent_deployments
		 WHERE agent_id = ? AND status = 'success' AND action = 'undeploy'
		 ORDER BY created_time DESC, id DESC LIMIT 1`, agentID,
	)
}

func (r *AgentDeploymentRepo) ListByStatus(ctx context.Context, status string) ([]*AgentDeployment, error) {
	return dbutil.QueryRowPtrs[AgentDeployment](ctx, r.db,
		`SELECT `+agentDeploymentCols+` FROM agent_deployments WHERE status = ? ORDER BY created_time ASC`, status,
	)
}
