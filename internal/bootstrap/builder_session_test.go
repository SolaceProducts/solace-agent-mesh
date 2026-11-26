// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func builderSessionURL(t *testing.T) string {
	t.Helper()
	merged, _, err := samapp.MergeBundledAppEntries(context.Background(), &config.YAMLLoader{}, nil, settings.BuilderConfigYAML())
	if err != nil {
		t.Fatalf("merge bundled builder: %v", err)
	}
	var builder map[string]any
	for _, app := range merged {
		if app.Name == "builder" {
			builder = app.AppConfig
		}
	}
	if builder == nil {
		t.Fatal("builder app not found in bundled entries")
	}
	sess, ok := builder["session_service"].(map[string]any)
	if !ok {
		t.Fatal("builder has no session_service block")
	}
	url, _ := sess["database_url"].(string)
	return url
}

func TestBuilderSessionService_UsesDatabaseURLWhenSet(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/sam_sessions")
	if got, want := builderSessionURL(t), "postgres://u:p@db:5432/sam_sessions"; got != want {
		t.Errorf("builder database_url = %q, want the shared Postgres URL (a hardcoded sqlite here pins every agent's sessions to an ephemeral file)", got)
	}
}

func TestBuilderSessionService_FallsBackToSqlite(t *testing.T) {
	t.Setenv("SAM_DATA_DIR", "/app/data")
	t.Setenv("DATABASE_URL", "")
	if err := os.Unsetenv("DATABASE_URL"); err != nil {
		t.Fatalf("Unsetenv(DATABASE_URL): %v", err)
	}
	if got, want := builderSessionURL(t), "sqlite:////app/data/sessions.db"; got != want {
		t.Errorf("builder database_url = %q, want %q", got, want)
	}
}
