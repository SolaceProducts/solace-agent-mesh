// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func sessionURLFromAWEBase(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sam.yaml")
	if err := os.WriteFile(path, settings.AWEClusterBaseConfigYAML(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := samapp.LoadFullConfig(context.Background(), &config.YAMLLoader{}, path)
	if err != nil {
		t.Fatalf("load awe_base.yaml: %v", err)
	}
	sess, ok := config.AsMap(cfg)["session_service"].(map[string]any)
	if !ok {
		t.Fatal("awe_base.yaml has no top-level session_service block")
	}
	url, _ := sess["database_url"].(string)
	return url
}

func TestAWEBase_SessionDefaultsToSharedSqlite(t *testing.T) {
	t.Setenv("SAM_DATA_DIR", "/app/data")

	for _, tc := range []struct{ name, val string }{
		{"unset", ""},
		{"empty", "set-empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.val == "set-empty" {
				t.Setenv("DATABASE_URL", "")
			} else {
				t.Setenv("DATABASE_URL", "")
				if err := os.Unsetenv("DATABASE_URL"); err != nil {
					t.Fatalf("Unsetenv(DATABASE_URL): %v", err)
				}
			}
			if got, want := sessionURLFromAWEBase(t), "sqlite:////app/data/sessions.db"; got != want {
				t.Errorf("database_url = %q, want %q", got, want)
			}
		})
	}
}

func TestAWEBase_SessionUsesDatabaseURLWhenSet(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/sam_sessions")

	if got, want := sessionURLFromAWEBase(t), "postgres://u:p@db:5432/sam_sessions"; got != want {
		t.Errorf("database_url = %q, want %q", got, want)
	}
}
