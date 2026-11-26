// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	claimMappingRedesignSQLiteVersion   = 68
	claimMappingRedesignPostgresVersion = 72
)

const (
	roleEditorID   = "10000000-0000-0000-0000-000000000001"
	roleViewerID   = "10000000-0000-0000-0000-000000000002"
	roleSecAdminID = "10000000-0000-0000-0000-000000000003"

	cmAzureEng1    = "00000000-0000-0000-0000-000000000001"
	cmAzureEng2    = "00000000-0000-0000-0000-000000000002"
	cmAzureSale    = "00000000-0000-0000-0000-000000000003"
	cmOktaEng      = "00000000-0000-0000-0000-000000000004"
	cmAzureEngDept = "00000000-0000-0000-0000-000000000005"
)

func rebind(pg bool, q string) string {
	if !pg {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func seedLegacyClaimMappings(t *testing.T, db *DB, pg bool) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, rebind(pg, q), args...); err != nil {
			t.Fatalf("seed exec %q: %v", q, err)
		}
	}

	roleQ := `INSERT INTO rbac_roles (id, name, description, created_by, updated_by, created_time, updated_time)
	          VALUES (?, ?, '', 'system', 'system', 1, 1)`
	exec(roleQ, roleEditorID, "Editor")
	exec(roleQ, roleViewerID, "Viewer")
	exec(roleQ, roleSecAdminID, "SecAdmin")

	mapQ := `INSERT INTO rbac_idp_claim_mappings
	         (id, oidc_provider, claim_key, claim_value, role_id, created_by, updated_by, created_time, updated_time)
	         VALUES (?, ?, ?, ?, ?, 'system', 'system', 1, 1)`
	exec(mapQ, cmAzureEng1, "azure", "groups", "eng", roleEditorID)
	exec(mapQ, cmAzureEng2, "azure", "groups", "eng", roleViewerID)
	exec(mapQ, cmAzureSale, "azure", "groups", "sales", roleEditorID)
	exec(mapQ, cmAzureEngDept, "azure", "department", "eng", roleSecAdminID)
	exec(mapQ, cmOktaEng, "okta", "roles", "eng", roleViewerID)
}

func assertRedesignBackfill(t *testing.T, db *DB) {
	t.Helper()
	snap, err := NewRBACRepo(db).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	byKey := make(map[string]*RBACIdpClaimMapping, len(snap.ClaimMappings))
	for _, m := range snap.ClaimMappings {
		byKey[m.OIDCProvider+"|"+m.ClaimValue] = m
	}
	if len(snap.ClaimMappings) != 3 {
		t.Fatalf("collapsed mappings = %d, want 3 (azure/eng, azure/sales, okta/eng): %+v", len(snap.ClaimMappings), snap.ClaimMappings)
	}

	cases := []struct {
		key, wantID, wantClaimKey string
		wantRoles                 []string
	}{
		{"azure|eng", cmAzureEng1, "groups", []string{roleEditorID, roleViewerID}},
		{"azure|sales", cmAzureSale, "groups", []string{roleEditorID}},
		{"okta|eng", cmOktaEng, "roles", []string{roleViewerID}},
	}
	for _, c := range cases {
		m := byKey[c.key]
		if m == nil {
			t.Errorf("missing collapsed mapping for %s", c.key)
			continue
		}
		if m.ID != c.wantID {
			t.Errorf("%s canonical id = %q, want %q (MIN legacy id)", c.key, m.ID, c.wantID)
		}
		if m.ClaimKey != c.wantClaimKey {
			t.Errorf("%s claim_key = %q, want %q (canonical row's own key)", c.key, m.ClaimKey, c.wantClaimKey)
		}
		got := append([]string(nil), snap.ClaimMappingRoles[m.ID]...)
		slices.Sort(got)
		want := append([]string(nil), c.wantRoles...)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s roles = %v, want %v", c.key, got, want)
		}
	}
}

func TestClaimMappingRedesign_SQLite_Backfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	url := "sqlite:///" + filepath.Join(t.TempDir(), "cm_backfill.db")
	db, err := Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), claimMappingRedesignSQLiteVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", claimMappingRedesignSQLiteVersion-1, err)
	}
	seedLegacyClaimMappings(t, db, false)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}
	assertRedesignBackfill(t, db)
}

func TestClaimMappingRedesign_Postgres_Backfill(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_PLATFORM_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_PLATFORM_DB_URL to a postgres URL")
	}

	ctx := context.Background()
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "platform_cm_backfill",
		func(u string) error {
			db, openErr := Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return dbutil.MigrateUpTo(ctx, db.migrateConfig(), claimMappingRedesignPostgresVersion-1)
		},
	)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)

	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	seedLegacyClaimMappings(t, db, true)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}
	assertRedesignBackfill(t, db)
}
