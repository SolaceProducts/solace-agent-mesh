// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	builtinRolesSQLiteVersion      = 71
	canonicalWildcardSQLiteVersion = 74
)

func TestRBACBuiltinRoleMigration_SeedsAndIsReversible(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "rbac_builtin.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), builtinRolesSQLiteVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", builtinRolesSQLiteVersion-1, err)
	}
	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), builtinRolesSQLiteVersion); err != nil {
		t.Fatalf("migrate up to %d (rbac builtin): %v", builtinRolesSQLiteVersion, err)
	}

	scopesAtSeed := []string{
		"agent:*:*", "agent_builder:*:*", "analytics:_:*", "builder:_:*",
		"connector:*:*", "deployment:_:*", "entrypoint:*:*", "evaluation:_:*",
		"model_config:*:*", "notify:_:*", "profile_provider:_:*", "skill:*:*",
		"tool:*:*", "toolset:*:*", "workflow:*:*", "workflow_builder:*:*",
	}
	scopesAtCanonical := []string{
		"agent:*:*", "agent_builder:*:*", "analytics:*:*", "builder:*:*",
		"connector:*:*", "deployment:*:*", "entrypoint:*:*", "evaluation:*:*",
		"model_config:*:*", "notify:*:*", "profile_provider:*:*", "skill:*:*",
		"tool:*:*", "toolset:*:*", "workflow:*:*", "workflow_builder:*:*",
	}
	scopesAtHead := []string{
		"*:*:read_usage", "agent:*:invoke", "agent:*:read_feedback", "agent_builder:*:*",
		"analytics:*:read", "budget:*:*", "builder:*:*", "connector:*:*", "deployment:*:*",
		"entrypoint:*:*", "evaluation:*:*", "model_config:*:*", "notify:*:send",
		"notify:*:send_others", "profile_provider:*:*", "skill:*:*", "tool:*:*",
		"toolset:*:*", "webui_settings:*:*", "workflow:*:invoke",
		"workflow:*:read_feedback", "workflow_builder:*:*",
	}
	assertSeed := func(stage string, wantScopes []string) {
		var builtin int
		if err := db.QueryRowContext(ctx,
			`SELECT builtin FROM rbac_roles WHERE name = 'sam_manager'`).Scan(&builtin); err != nil {
			t.Fatalf("%s: read sam_manager: %v", stage, err)
		}
		if builtin != 1 {
			t.Errorf("%s: sam_manager builtin = %d, want 1", stage, builtin)
		}
		rows, err := db.RawDB().QueryContext(ctx,
			`SELECT s.scope FROM rbac_role_scopes s JOIN rbac_roles r ON r.id = s.role_id WHERE r.name = 'sam_manager'`)
		if err != nil {
			t.Fatalf("%s: query sam_manager scopes: %v", stage, err)
		}
		defer func() { _ = rows.Close() }()
		var got []string
		for rows.Next() {
			var scope string
			if err := rows.Scan(&scope); err != nil {
				t.Fatalf("%s: scan scope: %v", stage, err)
			}
			if strings.HasPrefix(scope, "rbac:") {
				t.Errorf("%s: sam_manager must never hold an rbac scope, got %q", stage, scope)
			}
			got = append(got, scope)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("%s: iterate scopes: %v", stage, err)
		}
		slices.Sort(got)
		if !slices.Equal(got, wantScopes) {
			t.Errorf("%s: sam_manager scopes = %v, want %v", stage, got, wantScopes)
		}
	}
	assertSeed("after the seed migration", scopesAtSeed)

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), canonicalWildcardSQLiteVersion); err != nil {
		t.Fatalf("migrate up to %d (canonical wildcard): %v", canonicalWildcardSQLiteVersion, err)
	}
	assertSeed("after the canonical-wildcard migration", scopesAtCanonical)

	if _, err := db.RawDB().ExecContext(ctx,
		`INSERT INTO rbac_roles (id, name, created_by, updated_by, created_time, updated_time)
		 VALUES ('r2', 'defaulted', 'system', 'system', 1, 1)`); err != nil {
		t.Fatalf("insert defaulted role: %v", err)
	}
	var got int
	if err := db.QueryRowContext(ctx, `SELECT builtin FROM rbac_roles WHERE id = 'r2'`).Scan(&got); err != nil {
		t.Fatalf("read default builtin: %v", err)
	}
	if got != 0 {
		t.Errorf("default builtin = %d, want 0", got)
	}

	if err := db.MigrateDown(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-forward: %v", err)
	}
	assertSeed("after down+up", scopesAtHead)
}

func TestRBACBuiltinCanonicalWildcard_LeavesOtherRolesAlone(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "rbac_builtin_scoped.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	if upErr := dbutil.MigrateUpTo(ctx, db.migrateConfig(), canonicalWildcardSQLiteVersion-1); upErr != nil {
		t.Fatalf("migrate up to %d: %v", canonicalWildcardSQLiteVersion-1, upErr)
	}

	const customID = "11111111-1111-1111-1111-111111111111"
	baitScopes := []string{"connector:_:read", "evaluation:*:*"}
	if _, err := db.RawDB().ExecContext(ctx,
		`INSERT INTO rbac_roles (id, name, description, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'customer_role', '', 'seed', 'seed', 0, 0)`, customID); err != nil {
		t.Fatalf("seed customer role: %v", err)
	}
	for _, scope := range baitScopes {
		if _, err := db.RawDB().ExecContext(ctx,
			`INSERT INTO rbac_role_scopes (role_id, scope) VALUES (?, ?)`, customID, scope); err != nil {
			t.Fatalf("seed customer scope %q: %v", scope, err)
		}
	}

	customScopes := func(when string) []string {
		t.Helper()
		rows, qErr := db.RawDB().QueryContext(ctx,
			`SELECT scope FROM rbac_role_scopes WHERE role_id = ? ORDER BY scope`, customID)
		if qErr != nil {
			t.Fatalf("query customer scopes %s: %v", when, qErr)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var scope string
			if sErr := rows.Scan(&scope); sErr != nil {
				t.Fatalf("scan customer scope %s: %v", when, sErr)
			}
			out = append(out, scope)
		}
		if rErr := rows.Err(); rErr != nil {
			t.Fatalf("iterate customer scopes %s: %v", when, rErr)
		}
		return out
	}

	if upErr := dbutil.MigrateUpTo(ctx, db.migrateConfig(), canonicalWildcardSQLiteVersion); upErr != nil {
		t.Fatalf("migrate up to %d: %v", canonicalWildcardSQLiteVersion, upErr)
	}
	if got := customScopes("after up"); !slices.Equal(got, baitScopes) {
		t.Errorf("customer scopes after up = %v, want %v — the UP is not scoped to the sentinel role", got, baitScopes)
	}
	if !sentinelHasScope(ctx, t, db, "evaluation:*:*") {
		t.Fatal("sam_manager was not rewritten; the UP did not run")
	}

	if downErr := dbutil.MigrateDownTo(ctx, db.migrateConfig(), canonicalWildcardSQLiteVersion-1); downErr != nil {
		t.Fatalf("migrate down to %d: %v", canonicalWildcardSQLiteVersion-1, downErr)
	}
	if got := customScopes("after down"); !slices.Equal(got, baitScopes) {
		t.Errorf("customer scopes after down = %v, want %v — the DOWN is not scoped to the sentinel role", got, baitScopes)
	}
}

func sentinelHasScope(ctx context.Context, t *testing.T, db *DB, scope string) bool {
	t.Helper()
	var n int
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM rbac_role_scopes WHERE role_id = '00000000-0000-0000-0000-000000000001' AND scope = ?`,
		scope).Scan(&n); err != nil {
		t.Fatalf("count sentinel scope %q: %v", scope, err)
	}
	return n > 0
}

func TestRBACBuiltinCanonicalWildcard_DownRestoresOnlyTheSentinelScopes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "rbac_builtin_down.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()
	if upErr := dbutil.MigrateUpTo(ctx, db.migrateConfig(), canonicalWildcardSQLiteVersion); upErr != nil {
		t.Fatalf("migrate up to %d: %v", canonicalWildcardSQLiteVersion, upErr)
	}
	if downErr := dbutil.MigrateDownTo(ctx, db.migrateConfig(), canonicalWildcardSQLiteVersion-1); downErr != nil {
		t.Fatalf("migrate down to %d: %v", canonicalWildcardSQLiteVersion-1, downErr)
	}

	rows, err := db.RawDB().QueryContext(ctx,
		`SELECT scope FROM rbac_role_scopes WHERE role_id = '00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatalf("query sam_manager scopes: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			t.Fatalf("scan scope: %v", err)
		}
		got = append(got, scope)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate scopes: %v", err)
	}
	slices.Sort(got)

	want := []string{
		"agent:*:*", "agent_builder:*:*", "analytics:_:*", "builder:_:*",
		"connector:*:*", "deployment:_:*", "entrypoint:*:*", "evaluation:_:*",
		"model_config:*:*", "notify:_:*", "profile_provider:_:*", "skill:*:*",
		"tool:*:*", "toolset:*:*", "workflow:*:*", "workflow_builder:*:*",
	}
	if !slices.Equal(got, want) {
		t.Errorf("after rolling back the canonical-wildcard migration, sam_manager scopes = %v, want %v", got, want)
	}
}
