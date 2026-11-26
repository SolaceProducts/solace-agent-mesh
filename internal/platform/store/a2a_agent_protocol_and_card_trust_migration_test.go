// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"database/sql"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var a2aCardTrustColumns = []string{"protocol_version", "trusted_card_jwks_urls", "require_signed_card"}

func TestA2AAgentProtocolAndCardTrustMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "a2a_agent_protocol_and_card_trust", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		const legacyID = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO a2a_agents (id, name, agent_url, agent_card_location, agent_auth_type,
			agent_auth_config, created_by, updated_by, created_time, updated_time)
			VALUES (?, 'legacy-remote', 'https://agents.example.com/a2a', 'well_known', 'none', '{}', 'alice', 'alice', 1, 1)`, legacyID); err != nil {
			t.Fatalf("seed legacy a2a agent: %v", err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		var (
			pin     string
			urls    sql.NullString
			require bool
		)
		if err := db.QueryRowContext(ctx, `SELECT protocol_version, trusted_card_jwks_urls, require_signed_card FROM a2a_agents WHERE id = ?`, legacyID).
			Scan(&pin, &urls, &require); err != nil {
			t.Fatalf("read legacy row after up: %v", err)
		}
		if pin != "auto" || urls.Valid || require {
			t.Errorf("legacy row after up = %q %v %v, want auto, NULL and false", pin, urls, require)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, column := range a2aCardTrustColumns {
			if tableHasColumn(t, db, "a2a_agents", column) {
				t.Errorf("after down: a2a_agents.%s survived the rollback", column)
			}
		}
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM a2a_agents WHERE id = ?`, legacyID).Scan(&name); err != nil || name != "legacy-remote" {
			t.Errorf("legacy row after down = %q, %v; want it kept", name, err)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		for _, column := range a2aCardTrustColumns {
			if !tableHasColumn(t, db, "a2a_agents", column) {
				t.Errorf("after re-apply: a2a_agents.%s missing", column)
			}
		}
	})
}
