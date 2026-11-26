// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

const gatewaySessionKeysVersion = 13

func openFreshSQLite(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	url := "sqlite:///" + filepath.Join(dir, "backfill.db")
	db, err := Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open fresh sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBackfill_SQLite_Slack(t *testing.T) {
	t.Parallel()
	db := openFreshSQLite(t)
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, gatewaySessionKeysVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", gatewaySessionKeysVersion-1, err)
	}

	seed := []struct {
		id, want string
	}{
		{"slack-C0M9CMR3F-1234_5678", "C0M9CMR3F:1234.5678"},
		{"slack-C0M9CMR3F", "C0M9CMR3F:_"},
		{"slack-D-with-dashes-9999_0000", "D:with-dashes-9999.0000"},
	}
	for _, s := range seed {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, user_id, created_time, updated_time) VALUES (?, ?, 1, 1)`,
			s.id, "u1",
		); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	repo := NewGatewaySessionKeyRepo(db)
	for _, s := range seed {
		got, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeSlack, []string{s.want})
		if err != nil {
			t.Fatalf("lookup %s: %v", s.want, err)
		}
		if got[s.want] != s.id {
			t.Errorf("external_key %q → session_id %q, want %q", s.want, got[s.want], s.id)
		}
	}
}

func TestBackfill_SQLite_MCP(t *testing.T) {
	t.Parallel()
	db := openFreshSQLite(t)
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, gatewaySessionKeysVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", gatewaySessionKeysVersion-1, err)
	}

	seed := []struct {
		id, want string
	}{
		{"mcp-client-default", "default"},
		{"mcp-client-abc-123", "abc-123"},
	}
	for _, s := range seed {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, user_id, created_time, updated_time) VALUES (?, ?, 1, 1)`,
			s.id, "u1",
		); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	repo := NewGatewaySessionKeyRepo(db)
	for _, s := range seed {
		got, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeMCP, []string{s.want})
		if err != nil {
			t.Fatalf("lookup %s: %v", s.want, err)
		}
		if got[s.want] != s.id {
			t.Errorf("external_key %q → session_id %q, want %q", s.want, got[s.want], s.id)
		}
	}
}

func TestBackfill_SQLite_Scheduled(t *testing.T) {
	t.Parallel()
	db := openFreshSQLite(t)
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, gatewaySessionKeysVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", gatewaySessionKeysVersion-1, err)
	}

	execID := "01abcdef-2345-7890-abcd-ef0123456789"
	legacyID := "scheduled_" + execID
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_time, updated_time) VALUES (?, ?, 1, 1)`,
		legacyID, "u1",
	); err != nil {
		t.Fatalf("seed scheduled: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	repo := NewGatewaySessionKeyRepo(db)
	got, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeScheduled, []string{execID})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got[execID] != legacyID {
		t.Errorf("external_key %q → session_id %q, want %q", execID, got[execID], legacyID)
	}
}

func TestBackfill_SQLite_SoftDeletedSkipped(t *testing.T) {
	t.Parallel()
	db := openFreshSQLite(t)
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, gatewaySessionKeysVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", gatewaySessionKeysVersion-1, err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_time, updated_time, deleted_at) VALUES (?, ?, 1, 1, 2)`,
		"slack-Cdeleted-thread_ts", "u1",
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gateway_session_keys`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no back-fill for soft-deleted sessions, got %d rows", count)
	}
}

func TestBackfill_Postgres_Scheduled(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}

	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_backfill",
		func(u string) error {
			db, openErr := Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.MigrateUpTo(context.Background(), gatewaySessionKeysVersion-1)
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

	ctx := context.Background()
	execID := "01abcdef-2345-7890-abcd-ef0123456789"

	var sessionID string
	if scanErr := db.QueryRow(`SELECT md5('scheduled_' || $1)::uuid::text`, execID).Scan(&sessionID); scanErr != nil {
		t.Fatalf("compute session_id: %v", scanErr)
	}

	if _, seedErr := db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_time, updated_time) VALUES ($1, $2, 1, 1)`,
		sessionID, "u1",
	); seedErr != nil {
		t.Fatalf("seed session: %v", seedErr)
	}
	taskID := "01abcdef-2345-7890-abcd-ef0123456788"
	if _, taskErr := db.ExecContext(ctx,
		`INSERT INTO scheduled_tasks (id, name, namespace, created_by, schedule_type, schedule_expression,
			target_agent_name, task_message, source, created_at, updated_at)
		 VALUES ($1, 'task', 'ns', 'u1', 'cron', '* * * * *', 'agent', 'msg', 'ui', 1, 1)`,
		taskID,
	); taskErr != nil {
		t.Fatalf("seed scheduled_task: %v", taskErr)
	}
	if _, execErr := db.ExecContext(ctx,
		`INSERT INTO scheduled_task_executions (id, scheduled_task_id, status, scheduled_for)
		 VALUES ($1, $2, 'completed', 1)`,
		execID, taskID,
	); execErr != nil {
		t.Fatalf("seed execution: %v", execErr)
	}

	if migErr := db.Migrate(ctx); migErr != nil {
		t.Fatalf("complete migrations: %v", migErr)
	}

	repo := NewGatewaySessionKeyRepo(db)
	got, err := repo.GetByGatewayAndExternalKeys(ctx, GatewayTypeScheduled, []string{execID})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got[execID] != sessionID {
		t.Errorf("external_key %q → session_id %q, want %q", execID, got[execID], sessionID)
	}
}

func TestMigration_Postgres_HasActivityIdempotent(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}

	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_has_activity_idem",
		func(u string) error {
			db, openErr := Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.MigrateUpTo(context.Background(), sessionsHasActivityVersion-1)
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

	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN has_activity INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("simulate has_activity column: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN external_gateway INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatalf("simulate external_gateway column: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate to head must be idempotent on a pre-v30 column state: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'sessions'
		  AND column_name IN ('has_activity', 'external_gateway')`).Scan(&n); err != nil {
		t.Fatalf("verify columns: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected has_activity + external_gateway present, got %d", n)
	}
}
