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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

const (
	consumedTestSession = "00000000-0000-0000-0000-0000000000c1"
	consumedTestTask    = "10000000-0000-0000-0000-0000000000c1"
)

func appendConsumedTestRows(t *testing.T, repo *SSEEventRepo, taskID string, from, to int) {
	t.Helper()
	for seq := from; seq <= to; seq++ {
		if err := repo.Append(t.Context(), &SSEEventBufferRow{
			TaskID: taskID, SessionID: consumedTestSession, UserID: "u1",
			EventSequence: seq, EventType: "status_update", EventData: "{}",
		}); err != nil {
			t.Fatalf("append %d: %v", seq, err)
		}
	}
}

func unconsumedSeqs(t *testing.T, repo *SSEEventRepo, taskID string) []int {
	t.Helper()
	byTask, err := repo.GetUnconsumedBySession(t.Context(), consumedTestSession)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int
	for _, row := range byTask[taskID] {
		seqs = append(seqs, row.EventSequence)
	}
	return seqs
}

func TestSSEEventRepo_MarkConsumedInSession_OnlyConsumesThatSessionsEvents(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := t.Context()
	const otherSession = "00000000-0000-0000-0000-0000000000c2"

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 3)

	if err := repo.MarkConsumedInSession(ctx, consumedTestTask, otherSession); err != nil {
		t.Fatal(err)
	}
	if got := unconsumedSeqs(t, repo, consumedTestTask); len(got) != 3 {
		t.Fatalf("unconsumed after a save in another session = %v, want [1 2 3]", got)
	}

	if err := repo.MarkConsumedInSession(ctx, consumedTestTask, consumedTestSession); err != nil {
		t.Fatal(err)
	}
	if got := unconsumedSeqs(t, repo, consumedTestTask); len(got) != 0 {
		t.Fatalf("unconsumed after a save in the task's session = %v, want none", got)
	}
}

func TestSSEEventRepo_MarkConsumedLeavesLaterRowsUnconsumed(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := t.Context()

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 2)
	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	appendConsumedTestRows(t, repo, consumedTestTask, 3, 5)

	if got := unconsumedSeqs(t, repo, consumedTestTask); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("unconsumed = %v, want [3 4 5]", got)
	}
	rows, err := repo.GetByTask(ctx, consumedTestTask, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		wantConsumed := row.EventSequence <= 2
		if row.Consumed != wantConsumed || (row.ConsumedAt != nil) != wantConsumed {
			t.Errorf("seq %d consumed=%v consumedAt=%v, want consumed=%v", row.EventSequence, row.Consumed, row.ConsumedAt, wantConsumed)
		}
	}

	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if got := unconsumedSeqs(t, repo, consumedTestTask); len(got) != 0 {
		t.Fatalf("unconsumed after final save = %v, want none", got)
	}
}

func TestSSEEventRepo_MarkConsumedOnlyAdvances(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := t.Context()

	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatalf("MarkConsumed with no rows: %v", err)
	}
	var marks int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sse_task_consumed`).Scan(&marks); err != nil {
		t.Fatal(err)
	}
	if marks != 0 {
		t.Fatalf("a task with no buffered rows got %d consumed marks", marks)
	}

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 3)
	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	const aged = int64(1_000)
	if _, err := db.ExecContext(ctx, `UPDATE sse_task_consumed SET consumed_at = ?`, aged); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	var through int
	var at int64
	if err := db.QueryRowContext(ctx, `SELECT consumed_through_seq, consumed_at FROM sse_task_consumed WHERE task_id = ?`,
		consumedTestTask).Scan(&through, &at); err != nil {
		t.Fatal(err)
	}
	if through != 3 || at != aged {
		t.Fatalf("mark = (%d, %d), want (3, %d): a save that consumed nothing new must not restart the retention clock", through, at, aged)
	}
}

func TestSSEEventRepo_DeletesRemoveConsumedMarks(t *testing.T) {
	db := newTestDB(t)
	repo := NewSSEEventRepo(db)
	ctx := t.Context()
	marks := func() int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sse_task_consumed`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 2)
	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteByTask(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if n := marks(); n != 0 {
		t.Fatalf("DeleteByTask left %d consumed marks", n)
	}

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 3)
	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ? AND event_sequence <= 2`,
		time.Now().Add(-48*time.Hour).UnixMilli(), consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteOlderThan(ctx, time.Now().Add(-24*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if n := marks(); n != 1 {
		t.Fatalf("DeleteOlderThan removed the consumed mark of a task that still has rows: marks = %d", n)
	}
	if got := unconsumedSeqs(t, repo, consumedTestTask); len(got) != 0 {
		t.Fatalf("the remaining row lost its consumed state: unconsumed = %v", got)
	}
	if err := repo.DeleteByTask(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 2)
	if err := repo.MarkConsumed(ctx, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DeleteOlderThan(ctx, time.Now().Add(time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	if n := marks(); n != 0 {
		t.Fatalf("DeleteOlderThan left %d orphan consumed marks", n)
	}

	appendConsumedTestRows(t, repo, consumedTestTask, 1, 2)
	if got := unconsumedSeqs(t, repo, consumedTestTask); len(got) != 2 {
		t.Fatalf("rows buffered after the buffer was cleared read as consumed: unconsumed = %v", got)
	}
}

func consumedMigrationBody(t *testing.T, db *DB, version int64) {
	t.Helper()
	ctx := t.Context()
	const partial, untouched = "10000000-0000-0000-0000-0000000000c2", "10000000-0000-0000-0000-0000000000c3"
	for _, task := range []string{consumedTestTask, partial, untouched} {
		for seq := 1; seq <= 3; seq++ {
			if _, err := db.ExecContext(ctx,
				`INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at)
				 VALUES (?, ?, 'u1', ?, 'status_update', '{}', 1)`, task, consumedTestSession, seq); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE sse_event_buffer SET consumed = 1, consumed_at = 500 WHERE task_id = ?`, consumedTestTask); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE sse_event_buffer SET consumed = 1, consumed_at = 400 WHERE task_id = ? AND event_sequence <= 2`, partial); err != nil {
		t.Fatal(err)
	}

	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	repo := NewSSEEventRepo(db)
	for _, task := range []string{consumedTestTask, partial, untouched} {
		if got := unconsumedSeqs(t, repo, task); len(got) != 3 {
			t.Errorf("task %s unconsumed after migration = %v, want all 3 rows (per-row flags are not carried over)", task, got)
		}
	}

	if err := repo.MarkConsumed(ctx, untouched); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at)
		 VALUES (?, ?, 'u1', 4, 'status_update', '{}', 1)`, untouched, consumedTestSession); err != nil {
		t.Fatalf("append 4: %v", err)
	}
	var markedAt int64
	if err := db.QueryRowContext(ctx, `SELECT consumed_at FROM sse_task_consumed WHERE task_id = ?`, untouched).Scan(&markedAt); err != nil {
		t.Fatal(err)
	}

	if err := db.MigrateDownTo(ctx, version-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	var consumedRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sse_event_buffer WHERE consumed = 1`).Scan(&consumedRows); err != nil {
		t.Fatal(err)
	}
	if consumedRows != 8 {
		t.Fatalf("down migration left %d consumed rows, want 8: the 5 flagged before the upgrade plus the 3 marked after it", consumedRows)
	}
	rows, err := db.QueryContext(ctx, `SELECT event_sequence, consumed, consumed_at FROM sse_event_buffer WHERE task_id = ? ORDER BY event_sequence`, untouched)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq, consumed int
		var at *int64
		if err := rows.Scan(&seq, &consumed, &at); err != nil {
			t.Fatal(err)
		}
		if seq > 3 {
			if consumed != 0 {
				t.Errorf("seq %d above the mark was flagged consumed by the down migration", seq)
			}
			continue
		}
		if consumed != 1 || at == nil || *at != markedAt {
			t.Errorf("seq %d after down: consumed=%d consumed_at=%v, want 1 and the mark's %d", seq, consumed, at, markedAt)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUpTo(ctx, version); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

func TestSSETaskConsumedMigration(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "sse_consumed.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	version := migrationVersion(t, sqliteMigrations(), "sse_task_consumed")
	if err := db.MigrateUpTo(t.Context(), version-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	consumedMigrationBody(t, db, version)
}

func TestSSETaskConsumedMigration_Postgres(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	version := migrationVersion(t, postgresMigrations(), "sse_task_consumed")
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_sse_consumed", func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(context.Background(), version-1)
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
	consumedMigrationBody(t, db, version)
}
