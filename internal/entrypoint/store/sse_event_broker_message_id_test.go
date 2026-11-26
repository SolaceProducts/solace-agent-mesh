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

const sseBrokerMessageIDMigration = "sse_event_broker_message_id"

func sseBrokerMessageIDMigrationVersion(t *testing.T, db *DB) int64 {
	t.Helper()
	migrations := sqliteMigrations()
	if db.Dialect() == "postgres" {
		migrations = postgresMigrations()
	}
	return migrationVersion(t, migrations, sseBrokerMessageIDMigration)
}

func sseBrokerMessageIDMigrationBody(t *testing.T, db *DB) {
	t.Helper()
	ctx := t.Context()
	version := sseBrokerMessageIDMigrationVersion(t, db)
	const task = "10000000-0000-0000-0000-0000000000d1"
	previousInsert := `INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at)
		VALUES (?, ?, 'u1', ?, 'status_update', '{}', 1)`
	if _, err := db.ExecContext(ctx, previousInsert, task, consumedTestSession, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
	if _, err := db.ExecContext(ctx, previousInsert, task, consumedTestSession, 2); err != nil {
		t.Fatalf("the previous release's INSERT must still work after the migration: %v", err)
	}
	repo := NewSSEEventRepo(db)
	if err := repo.Append(ctx, &SSEEventBufferRow{
		TaskID: task, SessionID: consumedTestSession, UserID: "u1",
		EventSequence: 3, EventType: "status_update", EventData: "{}", BrokerMessageID: "rmid1:a-b-1",
	}); err != nil {
		t.Fatal(err)
	}
	if seq, err := repo.BrokerMessageSequence(ctx, task, "rmid1:a-b-1"); err != nil || seq != 3 {
		t.Fatalf("BrokerMessageSequence = %d, %v; want 3", seq, err)
	}
	var nulls int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sse_event_buffer WHERE task_id = ? AND broker_message_id IS NULL`, task).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 2 {
		t.Fatalf("rows without a message id = %d, want the previous release's 2", nulls)
	}

	if err := db.MigrateDownTo(ctx, version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT broker_message_id FROM sse_event_buffer`); err == nil {
		t.Fatal("broker_message_id survived the down migration")
	}
}

func TestSSEBrokerMessageIDMigration(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "sse_message_id.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateUpTo(t.Context(), sseBrokerMessageIDMigrationVersion(t, db)-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	sseBrokerMessageIDMigrationBody(t, db)
}

func TestSSEBrokerMessageIDMigration_Postgres(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_sse_message_id", func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(t.Context(), migrationVersion(t, postgresMigrations(), sseBrokerMessageIDMigration)-1)
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
	sseBrokerMessageIDMigrationBody(t, db)
}

func TestSSEEventRepo_SequenceState(t *testing.T) {
	db := TestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := t.Context()
	const task = "10000000-0000-0000-0000-0000000000d2"

	if maxSeq, done, err := repo.SequenceState(ctx, task); err != nil || maxSeq != 0 || done {
		t.Fatalf("empty task: %d %v %v", maxSeq, done, err)
	}
	appendConsumedTestRows(t, repo, task, 1, 4)
	if maxSeq, done, err := repo.SequenceState(ctx, task); err != nil || maxSeq != 4 || done {
		t.Fatalf("running task: %d %v %v; want 4, not completed", maxSeq, done, err)
	}
	if err := repo.MarkCompleted(ctx, task); err != nil {
		t.Fatal(err)
	}
	if maxSeq, done, err := repo.SequenceState(ctx, task); err != nil || maxSeq != 4 || !done {
		t.Fatalf("completed task: %d %v %v; want 4, completed", maxSeq, done, err)
	}
}

func TestSSEEventRepo_BrokerMessageIDRoundTrip(t *testing.T) {
	db := TestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := t.Context()
	const task = "10000000-0000-0000-0000-0000000000d3"
	rows := []*SSEEventBufferRow{
		{TaskID: task, SessionID: consumedTestSession, UserID: "u1", EventSequence: 1, EventType: "status_update", EventData: "{}", BrokerMessageID: "rmid1:x-1"},
		{TaskID: task, SessionID: consumedTestSession, UserID: "u1", EventSequence: 2, EventType: "status_update", EventData: "{}"},
	}
	if _, err := repo.AppendBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if seq, err := repo.BrokerMessageSequence(ctx, task, "rmid1:x-1"); err != nil || seq != 1 {
		t.Fatalf("BrokerMessageSequence = %d, %v; want 1", seq, err)
	}
	if seq, err := repo.BrokerMessageSequence(ctx, task, "rmid1:absent"); err != nil || seq != 0 {
		t.Fatalf("absent message: %d, %v; want 0", seq, err)
	}
	if seq, err := repo.BrokerMessageSequence(ctx, "10000000-0000-0000-0000-0000000000d4", "rmid1:x-1"); err != nil || seq != 0 {
		t.Fatalf("same message id under another task: %d, %v; want 0", seq, err)
	}
}
