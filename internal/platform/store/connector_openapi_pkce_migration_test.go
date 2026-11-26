// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type pkceMarkRow struct {
	id, connType, before, afterUp string
	updatedTimeBumped             bool
}

func pkceMarkRows() []pkceMarkRow {
	const mark = `{"auth_oauth_pkce":"disabled",`
	confidential := `"auth_oauth_authorization_url":"https://idp.example.com/authorize","auth_oauth_client_id":"orders","auth_oauth_token_endpoint_auth_method":"client_secret_basic","auth_type":"oauth","base_url":"https://api.example.com"}`
	pythonSpaced := `"auth_type": "oauth", "base_url": "https://api.example.com"}`
	noMethod := `"auth_type":"oauth","base_url":"https://api.example.com"}`
	public := `{"auth_oauth_token_endpoint_auth_method":"none","auth_type":"oauth","base_url":"https://api.example.com"}`
	publicSpaced := `{"auth_oauth_token_endpoint_auth_method": "none", "auth_type": "oauth"}`
	alreadySet := `{"auth_oauth_pkce":"enabled","auth_type":"oauth","base_url":"https://api.example.com"}`
	apiKey := `{"auth_apikey_name":"X-Key","auth_type":"apikey","base_url":"https://api.example.com"}`
	mcpOAuth := `{"auth_oauth_mode":"discovery","auth_type":"oauth","server_url":"https://mcp.example.com"}`
	malformed := `{"auth_type":"oauth","base_url":"https://api.example.com"`
	return []pkceMarkRow{
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a1", "api", "{" + confidential, mark + confidential, true},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a2", "api", "{" + pythonSpaced, mark + pythonSpaced, true},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a3", "api", "{" + noMethod, mark + noMethod, true},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a4", "api", public, public, true},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a5", "api", publicSpaced, publicSpaced, true},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a6", "api", alreadySet, alreadySet, true},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a7", "api", apiKey, apiKey, false},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a8", "mcp", mcpOAuth, mcpOAuth, false},
		{"0190f5ec-6f7a-7b8c-ad0e-0000000000a9", "api", malformed, mark + malformed[1:], true},
	}
}

func TestConnectorOpenAPIPKCEOptOutMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "connector_openapi_pkce_opt_out", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		rows := pkceMarkRows()
		for _, r := range rows {
			subtype := "openapi"
			if r.connType == "mcp" {
				subtype = "remote"
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO connectors (id, name, description, type, subtype, "values", created_by, updated_by, created_time, updated_time)
				VALUES (?, ?, '', ?, ?, ?, 'alice', 'alice', 1, 1)`, r.id, "conn-"+r.id, r.connType, subtype, r.before); err != nil {
				t.Fatalf("seed connector %s: %v", r.id, err)
			}
		}

		bumpWindowStart := time.Now().Add(-2 * time.Second).UnixMilli()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		for _, r := range rows {
			assertConnectorValues(t, db, "after up", r.id, r.afterUp)
			assertConnectorUpdatedTimeBumped(t, db, "after up", r.id, bumpWindowStart, r.updatedTimeBumped)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, r := range rows {
			assertConnectorValues(t, db, "after down", r.id, r.before)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		for _, r := range rows {
			assertConnectorValues(t, db, "after re-apply", r.id, r.afterUp)
			assertConnectorUpdatedTimeBumped(t, db, "after re-apply", r.id, bumpWindowStart, r.updatedTimeBumped)
		}
	})
}

func assertConnectorUpdatedTimeBumped(t *testing.T, db *DB, stage, id string, windowStart int64, want bool) {
	t.Helper()
	var got int64
	if err := db.QueryRowContext(t.Context(), `SELECT updated_time FROM connectors WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("%s: read connector %s updated_time: %v", stage, id, err)
	}
	if bumped := got >= windowStart; bumped != want {
		t.Errorf("%s: connector %s updated_time = %d, bumped to a current epoch-millisecond = %v, want %v", stage, id, got, bumped, want)
	}
}
