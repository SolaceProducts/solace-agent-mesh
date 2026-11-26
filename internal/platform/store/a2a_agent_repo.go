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

type A2AAgentRepo struct {
	db *DB
}

func NewA2AAgentRepo(db *DB) *A2AAgentRepo {
	return &A2AAgentRepo{db: db}
}

func (r *A2AAgentRepo) Create(ctx context.Context, a *A2AAgent) error {
	return insertA2AAgent(ctx, r.db, a)
}

func insertA2AAgent(ctx context.Context, q dbutil.Querier, a *A2AAgent) error {
	if a.ID == "" {
		a.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if a.CreatedTime == 0 {
		a.CreatedTime = now
	}
	if a.UpdatedTime == 0 {
		a.UpdatedTime = now
	}

	_, err := q.ExecContext(ctx,
		`INSERT INTO a2a_agents (id, agent_url, name, description,
			agent_card_location, agent_card_url, agent_card_auth_type, agent_card_auth_config,
			agent_card_headers, agent_auth_type, agent_auth_config, task_headers,
			request_timeout_seconds, skills, input_modes, output_modes,
			protocol_version, trusted_card_jwks_urls, require_signed_card,
			created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.AgentURL, a.Name, a.Description,
		a.AgentCardLocation, a.AgentCardURL, a.AgentCardAuthType, a.AgentCardAuthConfig,
		a.AgentCardHeaders, a.AgentAuthType, a.AgentAuthConfig, a.TaskHeaders,
		a.RequestTimeoutSeconds, a.Skills, a.InputModes, a.OutputModes,
		a.ProtocolVersion, a.TrustedCardJWKSURLs, dbutil.BoolToInt(a.RequireSignedCard),
		a.CreatedBy, a.UpdatedBy, a.CreatedTime, a.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("A2AAgentRepo.Create(%s): %w", a.ID, err)
	}
	return nil
}

func (r *A2AAgentRepo) GetByID(ctx context.Context, id string) (*A2AAgent, error) {
	return dbutil.QueryRow[A2AAgent](ctx, r.db,
		`SELECT id, agent_url, name, description,
			agent_card_location, agent_card_url, agent_card_auth_type, agent_card_auth_config,
			agent_card_headers, agent_auth_type, agent_auth_config, task_headers,
			request_timeout_seconds, skills, input_modes, output_modes,
			protocol_version, trusted_card_jwks_urls, require_signed_card,
			created_by, updated_by, created_time, updated_time
		 FROM a2a_agents WHERE id = ?`, id,
	)
}

func (r *A2AAgentRepo) GetByName(ctx context.Context, name string) (*A2AAgent, error) {
	return dbutil.QueryRow[A2AAgent](ctx, r.db,
		`SELECT id, agent_url, name, description,
			agent_card_location, agent_card_url, agent_card_auth_type, agent_card_auth_config,
			agent_card_headers, agent_auth_type, agent_auth_config, task_headers,
			request_timeout_seconds, skills, input_modes, output_modes,
			protocol_version, trusted_card_jwks_urls, require_signed_card,
			created_by, updated_by, created_time, updated_time
		 FROM a2a_agents WHERE LOWER(REPLACE(name, ' ', '')) = ?`, normalizeName(name),
	)
}

func (r *A2AAgentRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]*A2AAgent, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM a2a_agents`)
	if err != nil {
		return nil, 0, fmt.Errorf("A2AAgentRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	agents, err := dbutil.QueryRowPtrs[A2AAgent](ctx, r.db,
		`SELECT id, agent_url, name, description,
			agent_card_location, agent_card_url, agent_card_auth_type, agent_card_auth_config,
			agent_card_headers, agent_auth_type, agent_auth_config, task_headers,
			request_timeout_seconds, skills, input_modes, output_modes,
			protocol_version, trusted_card_jwks_urls, require_signed_card,
			created_by, updated_by, created_time, updated_time
		 FROM a2a_agents ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`,
		pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("A2AAgentRepo.ListPaginated query: %w", err)
	}

	return agents, total, nil
}

func (r *A2AAgentRepo) Update(ctx context.Context, a *A2AAgent) error {
	return updateA2AAgent(ctx, r.db, a)
}

func updateA2AAgent(ctx context.Context, q dbutil.Querier, a *A2AAgent) error {
	a.UpdatedTime = time.Now().UnixMilli()
	result, err := q.ExecContext(ctx,
		`UPDATE a2a_agents SET agent_url = ?, name = ?, description = ?,
			agent_card_location = ?, agent_card_url = ?, agent_card_auth_type = ?,
			agent_card_auth_config = ?, agent_card_headers = ?,
			agent_auth_type = ?, agent_auth_config = ?, task_headers = ?,
			request_timeout_seconds = ?, skills = ?, input_modes = ?, output_modes = ?,
			protocol_version = ?, trusted_card_jwks_urls = ?, require_signed_card = ?,
			updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		a.AgentURL, a.Name, a.Description,
		a.AgentCardLocation, a.AgentCardURL, a.AgentCardAuthType,
		a.AgentCardAuthConfig, a.AgentCardHeaders,
		a.AgentAuthType, a.AgentAuthConfig, a.TaskHeaders,
		a.RequestTimeoutSeconds, a.Skills, a.InputModes, a.OutputModes,
		a.ProtocolVersion, a.TrustedCardJWKSURLs, dbutil.BoolToInt(a.RequireSignedCard),
		a.UpdatedBy, a.UpdatedTime, a.ID,
	)
	if err != nil {
		return fmt.Errorf("update a2a agent: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("a2a agent not found")
	}
	return nil
}

func (r *A2AAgentRepo) Delete(ctx context.Context, id string) error {
	return deleteA2AAgent(ctx, r.db, id)
}

func deleteA2AAgent(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx, `DELETE FROM a2a_agents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("A2AAgentRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("a2a agent not found")
	}
	return nil
}
