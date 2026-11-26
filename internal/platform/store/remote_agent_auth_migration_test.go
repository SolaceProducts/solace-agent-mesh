// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestRemoteAgentAuthConfigMigration_Forward(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "sqlite" {
		t.Skip("exercises SQLite-specific migration SQL; Postgres variant lives in migration 00025")
	}
	ctx := context.Background()

	const rowID = "11111111-1111-1111-1111-111111111111"
	seedLegacyRow(t, db, rowID,
		"bearer", `{"bearerToken":"tok-bearer"}`,
		"apikey", `{"apiKey":"key-task","headerName":"X-API-Key"}`,
	)

	if _, err := db.RawDB().ExecContext(ctx, forwardMigrationSQL); err != nil {
		t.Fatalf("forward migration: %v", err)
	}

	card, agent := readConfigs(t, db, rowID)

	if card["type"] != "bearer" {
		t.Errorf("card type = %v, want bearer", card["type"])
	}
	if card["token"] != "tok-bearer" {
		t.Errorf("card token = %v, want tok-bearer", card["token"])
	}
	if _, legacy := card["bearerToken"]; legacy {
		t.Errorf("card config still has bearerToken: %v", card)
	}

	if agent["type"] != "apikey" {
		t.Errorf("agent type = %v, want apikey", agent["type"])
	}
	if agent["api_key"] != "key-task" {
		t.Errorf("agent api_key = %v, want key-task", agent["api_key"])
	}
	if agent["header_name"] != "X-API-Key" {
		t.Errorf("agent header_name = %v, want X-API-Key", agent["header_name"])
	}
}

func TestRemoteAgentAuthConfigMigration_Idempotent(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "sqlite" {
		t.Skip("exercises SQLite-specific migration SQL; Postgres variant lives in migration 00025")
	}
	ctx := context.Background()

	const rowID = "22222222-2222-2222-2222-222222222222"
	already := `{"type":"bearer","token":"already-migrated"}`
	seedLegacyRow(t, db, rowID, "bearer", already, "none", "{}")

	if _, err := db.RawDB().ExecContext(ctx, forwardMigrationSQL); err != nil {
		t.Fatalf("forward migration: %v", err)
	}

	var got sql.NullString
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT agent_card_auth_config FROM a2a_agents WHERE id = ?`,
		rowID,
	).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.String != already {
		t.Errorf("idempotent migration altered already-migrated row: got %q, want %q",
			got.String, already)
	}
}

func seedLegacyRow(t *testing.T, db *DB, id, cardType, cardCfg, agentType, agentCfg string) {
	t.Helper()
	if _, err := db.RawDB().ExecContext(context.Background(), `
		INSERT INTO a2a_agents (
			id, name, agent_url, agent_card_location,
			agent_card_auth_type, agent_card_auth_config,
			agent_auth_type, agent_auth_config,
			created_by, updated_by,
			created_time, updated_time
		) VALUES (?, ?, 'https://x', 'none', ?, ?, ?, ?,
			'tester', 'tester',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, id, "row-"+id[:8], cardType, cardCfg, agentType, agentCfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func readConfigs(t *testing.T, db *DB, id string) (card, agent map[string]any) {
	t.Helper()
	var cardStr, agentStr sql.NullString
	if err := db.RawDB().QueryRowContext(context.Background(),
		`SELECT agent_card_auth_config, agent_auth_config FROM a2a_agents WHERE id = ?`,
		id,
	).Scan(&cardStr, &agentStr); err != nil {
		t.Fatalf("select: %v", err)
	}
	if err := json.Unmarshal([]byte(cardStr.String), &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	if err := json.Unmarshal([]byte(agentStr.String), &agent); err != nil {
		t.Fatalf("unmarshal agent: %v", err)
	}
	return
}

var forwardMigrationSQL = strings.Join([]string{forwardCard, forwardAgent}, ";\n")

const forwardCard = `
UPDATE a2a_agents
SET agent_card_auth_config = CASE agent_card_auth_type
    WHEN 'bearer' THEN json_object(
        'type', 'bearer',
        'token', json_extract(agent_card_auth_config, '$.bearerToken')
    )
    WHEN 'apikey' THEN json_object(
        'type', 'apikey',
        'api_key', json_extract(agent_card_auth_config, '$.apiKey'),
        'header_name', COALESCE(json_extract(agent_card_auth_config, '$.headerName'), 'X-API-Key')
    )
    WHEN 'oauth2' THEN json_object(
        'type', 'oauth2',
        'client_id', json_extract(agent_card_auth_config, '$.clientId'),
        'client_secret', json_extract(agent_card_auth_config, '$.clientSecret'),
        'token_endpoint', COALESCE(
            json_extract(agent_card_auth_config, '$.tokenEndpoint'),
            json_extract(agent_card_auth_config, '$.tokenUrl')
        ),
        'scopes', COALESCE(json_extract(agent_card_auth_config, '$.scopes'), json('[]'))
    )
    WHEN 'basic' THEN json_object(
        'type', 'basic',
        'username', json_extract(agent_card_auth_config, '$.username'),
        'password', json_extract(agent_card_auth_config, '$.password')
    )
    ELSE agent_card_auth_config
END
WHERE agent_card_auth_config IS NOT NULL
  AND json_valid(agent_card_auth_config) = 1
  AND agent_card_auth_type IN ('bearer', 'apikey', 'oauth2', 'basic')
  AND json_extract(agent_card_auth_config, '$.type') IS NULL`

const forwardAgent = `
UPDATE a2a_agents
SET agent_auth_config = CASE agent_auth_type
    WHEN 'bearer' THEN json_object(
        'type', 'bearer',
        'token', json_extract(agent_auth_config, '$.bearerToken')
    )
    WHEN 'apikey' THEN json_object(
        'type', 'apikey',
        'api_key', json_extract(agent_auth_config, '$.apiKey'),
        'header_name', COALESCE(json_extract(agent_auth_config, '$.headerName'), 'X-API-Key')
    )
    WHEN 'oauth2' THEN json_object(
        'type', 'oauth2',
        'client_id', json_extract(agent_auth_config, '$.clientId'),
        'client_secret', json_extract(agent_auth_config, '$.clientSecret'),
        'token_endpoint', COALESCE(
            json_extract(agent_auth_config, '$.tokenEndpoint'),
            json_extract(agent_auth_config, '$.tokenUrl')
        ),
        'scopes', COALESCE(json_extract(agent_auth_config, '$.scopes'), json('[]'))
    )
    WHEN 'basic' THEN json_object(
        'type', 'basic',
        'username', json_extract(agent_auth_config, '$.username'),
        'password', json_extract(agent_auth_config, '$.password')
    )
    ELSE agent_auth_config
END
WHERE agent_auth_config IS NOT NULL
  AND json_valid(agent_auth_config) = 1
  AND agent_auth_type IN ('bearer', 'apikey', 'oauth2', 'basic')
  AND json_extract(agent_auth_config, '$.type') IS NULL`
