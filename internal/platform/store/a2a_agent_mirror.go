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

func prepareAgentForInsert(a *Agent) {
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
	if a.Type == "" {
		a.Type = AgentTypeStandard
	}
	if len(a.AdditionalConfigurations) == 0 {
		a.AdditionalConfigurations = []byte("{}")
	}
	a.Description = defaultStrPtr(a.Description, "")
}

func insertAgentRow(ctx context.Context, q dbutil.Querier, a *Agent) error {
	prepareAgentForInsert(a)
	_, err := q.ExecContext(ctx,
		`INSERT INTO agents (id, name, description, type, system_prompt, skills, skill_refs, model_provider, additional_configurations, created_by, updated_by, created_time, updated_time,
			is_ephemeral, expires_at, source_session_id, source_component_name, source_component_kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Description, a.Type, a.SystemPrompt, a.Skills, a.SkillRefs, a.ModelProvider, a.AdditionalConfigurations,
		a.CreatedBy, a.UpdatedBy, a.CreatedTime, a.UpdatedTime,
		dbutil.BoolToInt(a.IsEphemeral), a.ExpiresAt, a.SourceSessionID, a.SourceComponentName, a.SourceComponentKind,
	)
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	return nil
}

func mirrorOf(a *A2AAgent) *Agent {
	return &Agent{
		ID:          a.ID,
		Name:        a.Name,
		Description: a.Description,
		Type:        AgentTypeA2AProxy,
		CreatedBy:   a.CreatedBy,
		UpdatedBy:   a.UpdatedBy,
	}
}

func (r *A2AAgentRepo) CreateWithMirror(ctx context.Context, a *A2AAgent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := insertA2AAgent(ctx, tx, a); err != nil {
			return err
		}
		mirror := mirrorOf(a)
		mirror.CreatedTime, mirror.UpdatedTime = a.CreatedTime, a.UpdatedTime
		if err := insertAgentRow(ctx, tx, mirror); err != nil {
			return fmt.Errorf("A2AAgentRepo.CreateWithMirror(%s) mirror: %w", a.ID, err)
		}
		return nil
	})
}

func (r *A2AAgentRepo) UpdateWithMirror(ctx context.Context, a *A2AAgent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := updateA2AAgent(ctx, tx, a); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			`UPDATE agents SET name = ?, description = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
			a.Name, defaultStrPtr(a.Description, ""), a.UpdatedBy, a.UpdatedTime, a.ID,
		)
		if err != nil {
			return fmt.Errorf("A2AAgentRepo.UpdateWithMirror(%s) mirror: %w", a.ID, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("A2AAgentRepo.UpdateWithMirror(%s) mirror rows: %w", a.ID, err)
		}
		if updated > 0 {
			return nil
		}
		mirror := mirrorOf(a)
		mirror.CreatedTime, mirror.UpdatedTime = a.UpdatedTime, a.UpdatedTime
		if err := insertAgentRow(ctx, tx, mirror); err != nil {
			return fmt.Errorf("A2AAgentRepo.UpdateWithMirror(%s) create mirror: %w", a.ID, err)
		}
		return nil
	})
}

func (r *A2AAgentRepo) DeleteWithMirror(ctx context.Context, id string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := deleteA2AAgent(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id); err != nil {
			return fmt.Errorf("A2AAgentRepo.DeleteWithMirror(%s) mirror: %w", id, err)
		}
		return nil
	})
}
