// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	webuiSettingsTableSQLiteVersion = 75
	webuiSettingsScopeSQLiteVersion = 76
)

func TestPlatformWebuiSettingsMigration_RoundTrip(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "webui_settings.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := t.Context()

	if migrateErr := dbutil.MigrateUpTo(ctx, db.migrateConfig(), webuiSettingsTableSQLiteVersion-1); migrateErr != nil {
		t.Fatalf("migrate up to %d: %v", webuiSettingsTableSQLiteVersion-1, migrateErr)
	}
	if migrateErr := dbutil.MigrateUpTo(ctx, db.migrateConfig(), webuiSettingsTableSQLiteVersion); migrateErr != nil {
		t.Fatalf("migrate up to %d (webui settings table): %v", webuiSettingsTableSQLiteVersion, migrateErr)
	}

	if n := webuiSettingsRowCount(t, db); n != 0 {
		t.Fatalf("table must ship empty (no seeding), got %d rows", n)
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), webuiSettingsScopeSQLiteVersion); err != nil {
		t.Fatalf("migrate up to %d (rbac scope grant): %v", webuiSettingsScopeSQLiteVersion, err)
	}
	scopes := samManagerScopes(t, db)
	if !containsScope(scopes, "webui_settings:*:*") {
		t.Errorf("sam_manager scopes after 00076 = %v, want webui_settings:*:* present", scopes)
	}

	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), webuiSettingsScopeSQLiteVersion-1); err != nil {
		t.Fatalf("migrate down past scope grant: %v", err)
	}
	scopes = samManagerScopes(t, db)
	if containsScope(scopes, "webui_settings:*:*") {
		t.Errorf("sam_manager scopes after rollback = %v, want webui_settings:*:* removed", scopes)
	}

	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), webuiSettingsTableSQLiteVersion-1); err != nil {
		t.Fatalf("migrate down past table creation: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM platform_webui_settings`).Scan(new(int)); err == nil {
		t.Fatal("expected an error querying a dropped table")
	}

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), webuiSettingsScopeSQLiteVersion); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	if n := webuiSettingsRowCount(t, db); n != 0 {
		t.Fatalf("re-applied table row count = %d, want 0", n)
	}
	scopes = samManagerScopes(t, db)
	if !containsScope(scopes, "webui_settings:*:*") {
		t.Errorf("sam_manager scopes after re-apply = %v, want webui_settings:*:* present", scopes)
	}
}

func webuiSettingsRowCount(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM platform_webui_settings`).Scan(&n); err != nil {
		t.Fatalf("count platform_webui_settings: %v", err)
	}
	return n
}

func samManagerScopes(t *testing.T, db *DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT scope FROM rbac_role_scopes WHERE role_id = '00000000-0000-0000-0000-000000000001'`)
	if err != nil {
		t.Fatalf("query role scopes: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func containsScope(scopes []string, target string) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}
