// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

const taskRecordsInstanceMigration = "task_records_instance_and_waiting"

func taskRecordsInstanceMigrationVersion(t *testing.T, db *DB) int64 {
	t.Helper()
	migrations := sqliteMigrations()
	if db.Dialect() == "postgres" {
		migrations = postgresMigrations()
	}
	return migrationVersion(t, migrations, taskRecordsInstanceMigration)
}

func taskRecordsInstanceMigrationBody(t *testing.T, db *DB) {
	t.Helper()
	ctx := t.Context()
	version := taskRecordsInstanceMigrationVersion(t, db)
	const id = "10000000-0000-0000-0000-0000000000b1"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_records (task_id, gateway_id, agent_name, user_id, last_seen_at, created_at)
		 VALUES (?, 'webui', 'agent-a', 'u1', 1, 1)`, id); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	rec, err := NewTaskRecordRepo(db, "webui").Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.GatewayInstanceID != "" || rec.WaitingRequestID != nil {
		t.Fatalf("a row from before the migration is taken by nobody and has no waiting flag: %+v", rec)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_records (task_id, gateway_id, agent_name, user_id, last_seen_at, created_at)
		 VALUES (?, 'webui', 'agent-a', 'u1', 1, 1)`, "10000000-0000-0000-0000-0000000000b2"); err != nil {
		t.Fatalf("the previous release's INSERT must still work after the migration: %v", err)
	}

	if err := db.MigrateDownTo(ctx, version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT gateway_instance_id FROM task_records`); err == nil {
		t.Fatal("gateway_instance_id survived the down migration")
	}
}

func TestTaskRecordsInstanceMigration(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "task_records_instance.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateUpTo(t.Context(), taskRecordsInstanceMigrationVersion(t, db)-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	taskRecordsInstanceMigrationBody(t, db)
}

func TestTaskRecordsInstanceMigration_Postgres(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_task_records_instance", func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(t.Context(), migrationVersion(t, postgresMigrations(), taskRecordsInstanceMigration)-1)
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)
	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	taskRecordsInstanceMigrationBody(t, db)
}
