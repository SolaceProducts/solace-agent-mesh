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

const (
	SystemUserToolAuthPending      = "pending"
	SystemUserToolAuthConnected    = "connected"
	SystemUserToolAuthDisconnected = "disconnected"
)

type SystemUserToolAuth struct {
	Principal             string  `db:"principal"`
	AgentName             string  `db:"agent_name"`
	CredentialKey         string  `db:"credential_key"`
	State                 string  `db:"state"`
	BegunBy               *string `db:"begun_by"`
	BegunTime             *int64  `db:"begun_time"`
	BegunRef              *string `db:"begun_ref"`
	AuthorizedBy          *string `db:"authorized_by"`
	AuthorizedTime        *int64  `db:"authorized_time"`
	ConnectedObservedTime *int64  `db:"connected_observed_time"`
	DisconnectedBy        *string `db:"disconnected_by"`
	DisconnectedTime      *int64  `db:"disconnected_time"`
}

type SystemUserToolAuthKey struct {
	Principal     string
	AgentName     string
	CredentialKey string
}

const systemUserToolAuthColumns = `principal, agent_name, credential_key, state, begun_by, begun_time,
	begun_ref, authorized_by, authorized_time, connected_observed_time,
	disconnected_by, disconnected_time`

func ListSystemUserToolAuth(ctx context.Context, db *DB, principal, agentName string) (map[string]SystemUserToolAuth, error) {
	rows, err := dbutil.QueryRows[SystemUserToolAuth](ctx, db,
		`SELECT `+systemUserToolAuthColumns+` FROM system_user_tool_auth WHERE principal = ? AND agent_name = ?`,
		principal, agentName)
	if err != nil {
		return nil, fmt.Errorf("list system user tool auth for agent %s: %w", agentName, err)
	}
	byKey := make(map[string]SystemUserToolAuth, len(rows))
	for _, row := range rows {
		byKey[row.CredentialKey] = row
	}
	return byKey, nil
}

func BeginSystemUserToolAuth(ctx context.Context, db *DB, key SystemUserToolAuthKey, begunBy string, begunTime int64, begunRef string) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO system_user_tool_auth (principal, agent_name, credential_key, state, begun_by, begun_time, begun_ref)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (principal, agent_name, credential_key)
		 DO UPDATE SET state = EXCLUDED.state, begun_by = EXCLUDED.begun_by,
		   begun_time = EXCLUDED.begun_time, begun_ref = EXCLUDED.begun_ref`,
		key.Principal, key.AgentName, key.CredentialKey, SystemUserToolAuthPending, begunBy, begunTime, begunRef,
	); err != nil {
		return fmt.Errorf("record system user tool auth begin on agent %s: %w", key.AgentName, err)
	}
	return nil
}

func CompleteSystemUserToolAuth(ctx context.Context, db *DB, key SystemUserToolAuthKey, begunRef string, observedTime int64) (bool, error) {
	result, err := db.ExecContext(ctx,
		`UPDATE system_user_tool_auth
		 SET state = ?, authorized_by = begun_by, authorized_time = begun_time, connected_observed_time = ?, begun_ref = NULL
		 WHERE principal = ? AND agent_name = ? AND credential_key = ? AND begun_ref = ?`,
		SystemUserToolAuthConnected, observedTime,
		key.Principal, key.AgentName, key.CredentialKey, begunRef,
	)
	if err != nil {
		return false, fmt.Errorf("record system user tool auth completion on agent %s: %w", key.AgentName, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count system user tool auth completion on agent %s: %w", key.AgentName, err)
	}
	return affected == 1, nil
}

func DisconnectSystemUserToolAuth(ctx context.Context, db *DB, key SystemUserToolAuthKey, disconnectedBy string, disconnectedTime int64) error {
	if _, err := db.ExecContext(ctx,
		`INSERT INTO system_user_tool_auth (principal, agent_name, credential_key, state, disconnected_by, disconnected_time)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (principal, agent_name, credential_key)
		 DO UPDATE SET state = EXCLUDED.state, disconnected_by = EXCLUDED.disconnected_by,
		   disconnected_time = EXCLUDED.disconnected_time`,
		key.Principal, key.AgentName, key.CredentialKey, SystemUserToolAuthDisconnected, disconnectedBy, disconnectedTime,
	); err != nil {
		return fmt.Errorf("record system user tool auth disconnect on agent %s: %w", key.AgentName, err)
	}
	return nil
}
