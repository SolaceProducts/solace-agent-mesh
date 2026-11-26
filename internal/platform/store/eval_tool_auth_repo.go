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

type ToolAuthAttribution struct {
	ExperimentID   string `db:"experiment_id"`
	AgentName      string `db:"agent_name"`
	CredentialKey  string `db:"credential_key"`
	AuthorizedBy   string `db:"authorized_by"`
	AuthorizedTime int64  `db:"authorized_time"`
}

type ToolAuthAttributionKey struct {
	AgentName     string
	CredentialKey string
}

func UpsertToolAuthAttribution(ctx context.Context, db *DB, experimentID, agentName, credentialKey, authorizedBy string, authorizedTime int64) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_tool_auth_attribution (experiment_id, agent_name, credential_key, authorized_by, authorized_time)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (experiment_id, agent_name, credential_key)
		 DO UPDATE SET authorized_by = EXCLUDED.authorized_by, authorized_time = EXCLUDED.authorized_time`,
		experimentID, agentName, credentialKey, authorizedBy, authorizedTime,
	); err != nil {
		return fmt.Errorf("upsert tool auth attribution for experiment %s: %w", experimentID, err)
	}
	return nil
}

func ListToolAuthAttribution(ctx context.Context, db *DB, experimentID string) (map[ToolAuthAttributionKey]ToolAuthAttribution, error) {
	rows, err := dbutil.QueryRows[ToolAuthAttribution](ctx, db,
		`SELECT experiment_id, agent_name, credential_key, authorized_by, authorized_time
		 FROM eval_tool_auth_attribution WHERE experiment_id = ?`, experimentID)
	if err != nil {
		return nil, fmt.Errorf("list tool auth attribution for experiment %s: %w", experimentID, err)
	}
	byKey := make(map[ToolAuthAttributionKey]ToolAuthAttribution, len(rows))
	for _, row := range rows {
		byKey[ToolAuthAttributionKey{AgentName: row.AgentName, CredentialKey: row.CredentialKey}] = row
	}
	return byKey, nil
}

func DeleteToolAuthAttribution(ctx context.Context, db *DB, experimentID, agentName, credentialKey string) error {
	if _, err := db.ExecContext(ctx,
		`DELETE FROM eval_tool_auth_attribution
		 WHERE experiment_id = ? AND agent_name = ? AND credential_key = ?`,
		experimentID, agentName, credentialKey,
	); err != nil {
		return fmt.Errorf("delete tool auth attribution for experiment %s: %w", experimentID, err)
	}
	return nil
}

type ToolAuthAgent struct {
	ExperimentID string `db:"experiment_id"`
	AgentName    string `db:"agent_name"`
	AddedBy      string `db:"added_by"`
	AddedTime    int64  `db:"added_time"`
}

func AddToolAuthAgent(ctx context.Context, db *DB, experimentID, agentName, addedBy string, addedTime int64) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO eval_tool_auth_agents (experiment_id, agent_name, added_by, added_time)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (experiment_id, agent_name) DO NOTHING`,
		experimentID, agentName, addedBy, addedTime,
	); err != nil {
		return fmt.Errorf("add tool auth agent for experiment %s: %w", experimentID, err)
	}
	return nil
}

func ListToolAuthAgents(ctx context.Context, db *DB, experimentID string) ([]ToolAuthAgent, error) {
	rows, err := dbutil.QueryRows[ToolAuthAgent](ctx, db,
		`SELECT experiment_id, agent_name, added_by, added_time
		 FROM eval_tool_auth_agents WHERE experiment_id = ?
		 ORDER BY added_time, agent_name`, experimentID)
	if err != nil {
		return nil, fmt.Errorf("list tool auth agents for experiment %s: %w", experimentID, err)
	}
	return rows, nil
}

func RemoveToolAuthAgent(ctx context.Context, db *DB, experimentID, agentName string) (bool, error) {
	result, err := db.ExecContext(ctx,
		`DELETE FROM eval_tool_auth_agents WHERE experiment_id = ? AND agent_name = ?`,
		experimentID, agentName,
	)
	if err != nil {
		return false, fmt.Errorf("remove tool auth agent for experiment %s: %w", experimentID, err)
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}
