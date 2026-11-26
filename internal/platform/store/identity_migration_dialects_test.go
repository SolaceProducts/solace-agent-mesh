// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

const (
	userIdentityClaimsPostgresVersion = 86

	entrypointBindingsSQLiteVersion   = 83
	entrypointBindingsPostgresVersion = 87

	bindingRotatedAtSQLiteVersion   = 84
	bindingRotatedAtPostgresVersion = 88
)

var emptyPostgresTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL(platformTestDBURL(), "platform_identity_migrations", func(string) error { return nil })
})

func platformTestDBURL() string { return os.Getenv("SAM_TEST_PLATFORM_DB_URL") }

func postgresLaneActive() bool {
	u := platformTestDBURL()
	return strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://")
}

func forEachPlatformDialect(t *testing.T, sqliteVersion, postgresVersion int64, fn func(t *testing.T, db *DB, version int64)) {
	t.Helper()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()
		db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "identity_migration.db"), nil)
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		fn(t, db, sqliteVersion)
	})

	t.Run("postgres", func(t *testing.T) {
		if !postgresLaneActive() {
			t.Skip("postgres lane not configured; set SAM_TEST_PLATFORM_DB_URL")
		}
		t.Parallel()
		tmpl, err := emptyPostgresTemplate()
		if err != nil {
			t.Fatalf("postgres template: %v", err)
		}
		db, err := Open(tmpl.Clone(t), nil)
		if err != nil {
			t.Fatalf("open postgres clone: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		fn(t, db, postgresVersion)
	})
}
