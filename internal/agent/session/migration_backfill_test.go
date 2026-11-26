// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const sessionUUIDVersion = 3

func openFreshSQLiteSession(t *testing.T) *dbutil.DB {
	t.Helper()
	dir := t.TempDir()
	url := "sqlite:///" + filepath.Join(dir, "session-backfill.db")
	db, err := dbutil.Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open fresh sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSessionBackfill_SQLite_RunComposite(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.MigrateUpTo(ctx, sessionUUIDVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", sessionUUIDVersion-1, err)
	}

	parent := "11111111-2222-7333-8444-555555555555"
	taskUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	composite := parent + ":" + taskUUID + ":run"

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, 0, '[]', '{}', '0', '0')`,
		"app", "u1", composite,
	); err != nil {
		t.Fatalf("seed run-based session: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_checkpoints (task_id, app_name, user_id, session_id, agent_name, pending_count, state, tool_results, claimed, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '', 0, NULL, '[]', 0, '0', '0')`,
		"99999999-aaaa-bbbb-cccc-000000000001", "app", "u1", composite,
	); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE session_id = ?`, composite).Scan(&count); err != nil {
		t.Fatalf("count old: %v", err)
	}
	if count != 0 {
		t.Errorf("expected composite session_id to be replaced, found %d rows", count)
	}

	var newID, contextID, taskID string
	if err := db.QueryRowContext(ctx,
		`SELECT session_id, context_id, COALESCE(task_id, '') FROM sessions WHERE app_name = ? AND user_id = ?`,
		"app", "u1",
	).Scan(&newID, &contextID, &taskID); err != nil {
		t.Fatalf("read remapped row: %v", err)
	}
	if contextID != parent {
		t.Errorf("context_id = %q, want %q", contextID, parent)
	}
	if taskID != taskUUID {
		t.Errorf("task_id = %q, want %q", taskID, taskUUID)
	}
	if newID == composite {
		t.Errorf("session_id was not rewritten: %q", newID)
	}

	var checkpointSessionID string
	if err := db.QueryRowContext(ctx,
		`SELECT session_id FROM task_checkpoints WHERE app_name = ? AND user_id = ?`,
		"app", "u1",
	).Scan(&checkpointSessionID); err != nil {
		t.Fatalf("read remapped checkpoint: %v", err)
	}
	if checkpointSessionID != newID {
		t.Errorf("checkpoint session_id = %q, want %q (matching sessions row)", checkpointSessionID, newID)
	}
}

func TestSessionBackfill_SQLite_Default(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.MigrateUpTo(ctx, sessionUUIDVersion-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	rows := []struct{ app, user string }{
		{"app1", "u1"},
		{"app1", "u2"},
		{"app2", "u1"},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
			 VALUES (?, ?, 'default', 0, '[]', '{}', '0', '0')`,
			r.app, r.user,
		); err != nil {
			t.Fatalf("seed default %v: %v", r, err)
		}
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE session_id = 'default'`).Scan(&count); err != nil {
		t.Fatalf("count default: %v", err)
	}
	if count != 0 {
		t.Errorf("expected zero 'default' rows after migration, got %d", count)
	}

	seen := map[string]string{}
	for _, r := range rows {
		var newID, contextID, taskID string
		err := db.QueryRowContext(ctx,
			`SELECT session_id, context_id, COALESCE(task_id, '') FROM sessions WHERE app_name = ? AND user_id = ?`,
			r.app, r.user,
		).Scan(&newID, &contextID, &taskID)
		if err != nil {
			t.Fatalf("read remapped %v: %v", r, err)
		}
		if newID == "" || newID == "default" {
			t.Errorf("%v new session_id = %q, expected fresh UUID", r, newID)
		}
		if contextID != newID {
			t.Errorf("%v context_id = %q, want %q (self-context for promoted default)", r, contextID, newID)
		}
		if taskID != "" {
			t.Errorf("%v task_id = %q, want empty (default rows are normal sessions)", r, taskID)
		}
		if prev, ok := seen[newID]; ok {
			t.Errorf("default remap collision: tuple %v and %s both got UUID %q", r, prev, newID)
		}
		seen[newID] = r.app + "/" + r.user
	}
}

func TestSessionBackfill_SQLite_NormalRowGetsContextID(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.MigrateUpTo(ctx, sessionUUIDVersion-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	uuid := "11111111-2222-7333-8444-666666666666"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, 0, '[]', '{}', '0', '0')`,
		"app", "u1", uuid,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var sid, contextID, taskID string
	if err := db.QueryRowContext(ctx,
		`SELECT session_id, context_id, COALESCE(task_id, '') FROM sessions WHERE app_name = ? AND user_id = ?`,
		"app", "u1",
	).Scan(&sid, &contextID, &taskID); err != nil {
		t.Fatalf("read: %v", err)
	}
	if sid != uuid {
		t.Errorf("session_id changed: %q, want %q (normal rows preserve their UUID)", sid, uuid)
	}
	if contextID != uuid {
		t.Errorf("context_id = %q, want %q (== session_id for normal)", contextID, uuid)
	}
	if taskID != "" {
		t.Errorf("task_id = %q, want empty (normal session has no spawning task)", taskID)
	}
}

func TestSessionBackfill_SQLite_OrphanCheckpointDeleted(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.MigrateUpTo(ctx, sessionUUIDVersion-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_checkpoints (task_id, app_name, user_id, session_id, agent_name, pending_count, state, tool_results, claimed, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '', 0, NULL, '[]', 0, '0', '0')`,
		"99999999-aaaa-bbbb-cccc-000000000002",
		"app", "u1", "11111111-2222-7333-8444-666666666666",
	); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_checkpoints`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected orphan checkpoint to be deleted, got %d rows", count)
	}
}

func TestSessionBackfill_SQLite_UniqueContextTaskIndex(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, context_id, task_id, version, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, '{}', '0', '0')`,
		"app", "u1", "aaaaaaaa-bbbb-7ccc-8ddd-000000000001",
		"11111111-2222-7333-8444-555555555555",
		"22222222-3333-7444-8555-666666666666",
	); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, context_id, task_id, version, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, '{}', '0', '0')`,
		"app", "u1", "aaaaaaaa-bbbb-7ccc-8ddd-000000000002",
		"11111111-2222-7333-8444-555555555555",
		"22222222-3333-7444-8555-666666666666",
	); err == nil {
		t.Errorf("expected unique-index violation, got nil")
	}
}

type pgBackfillTemplateResult struct {
	tmpl *dbtest.Template
	err  error
}

var pgBackfillTemplate = sync.OnceValue(func() pgBackfillTemplateResult {
	dbURL := os.Getenv("SAM_TEST_SESSION_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		return pgBackfillTemplateResult{}
	}
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "session_backfill",
		func(u string) error {
			db, openErr := dbutil.Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return NewSQLStore(db).MigrateUpTo(context.Background(), sessionUUIDVersion-1)
		},
	)
	return pgBackfillTemplateResult{tmpl: tmpl, err: err}
})

func postgresTemplate(t *testing.T) *dbtest.Template {
	t.Helper()
	res := pgBackfillTemplate()
	if res.tmpl == nil && res.err == nil {
		t.Skip("postgres-only test; set SAM_TEST_SESSION_DB_URL")
	}
	if res.err != nil {
		t.Fatalf("template: %v", res.err)
	}
	return res.tmpl
}

func TestSessionBackfill_Postgres_DefaultPerUser(t *testing.T) {
	t.Parallel()
	tmpl := postgresTemplate(t)

	db, err := dbutil.Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(db)
	ctx := context.Background()

	rows := []struct{ app, user string }{
		{"app1", "u1"},
		{"app1", "u2"},
		{"app2", "u1"},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
			 VALUES ($1, $2, 'default', 0, '[]', '{}', '0', '0')`,
			r.app, r.user,
		); err != nil {
			t.Fatalf("seed default %v: %v", r, err)
		}
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	seen := map[string]string{}
	for _, r := range rows {
		var newID, contextID string
		var taskID *string
		if scanErr := db.QueryRowContext(ctx,
			`SELECT session_id::text, context_id::text, task_id::text FROM sessions WHERE app_name = $1 AND user_id = $2`,
			r.app, r.user,
		).Scan(&newID, &contextID, &taskID); scanErr != nil {
			t.Fatalf("read remapped %v: %v", r, scanErr)
		}
		if contextID != newID {
			t.Errorf("%v context_id = %q, want %q (self for default-promoted normal)", r, contextID, newID)
		}
		if taskID != nil {
			t.Errorf("%v task_id = %v, want NULL", r, *taskID)
		}
		if prev, ok := seen[newID]; ok {
			t.Errorf("default remap collision on Postgres: %v and %s both got %q", r, prev, newID)
		}
		seen[newID] = r.app + "/" + r.user
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 default-derived rows, got %d", count)
	}
}

func TestSessionBackfill_Postgres_FK(t *testing.T) {
	t.Parallel()
	tmpl := postgresTemplate(t)

	db, err := dbutil.Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(db)
	ctx := context.Background()

	parent := "11111111-2222-7333-8444-555555555555"
	taskUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	composite := parent + ":" + taskUUID + ":run"

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, 0, '[]', '{}', '0', '0')`,
		"app", "u1", composite,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	_, fkErr := db.ExecContext(ctx,
		`INSERT INTO task_checkpoints (task_id, app_name, user_id, session_id, agent_name, pending_count, state, tool_results, claimed, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, '', 0, NULL, '[]', 0, '0', '0')`,
		"99999999-aaaa-bbbb-cccc-000000000003",
		"app", "u1", "ffffffff-ffff-7fff-8fff-ffffffffffff",
	)
	if fkErr == nil {
		t.Errorf("expected FK violation, got nil error")
	}
}

func TestSessionBackfill_Postgres_RunCompositeContextAndTask(t *testing.T) {
	t.Parallel()
	tmpl := postgresTemplate(t)

	db, err := dbutil.Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(db)
	ctx := context.Background()

	parent := "11111111-2222-7333-8444-555555555555"
	taskUUID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	composite := parent + ":" + taskUUID + ":run"

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, 0, '[]', '{}', '0', '0')`,
		"app", "u1", composite,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	checkpointTaskID := "99999999-aaaa-bbbb-cccc-000000000004"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO task_checkpoints (task_id, app_name, user_id, session_id, agent_name, pending_count, state, tool_results, claimed, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, '', 0, NULL, '[]', 0, '0', '0')`,
		checkpointTaskID, "app", "u1", composite,
	); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var newID, contextID, taskID string
	if err := db.QueryRowContext(ctx,
		`SELECT session_id::text, context_id::text, task_id::text FROM sessions WHERE app_name = $1 AND user_id = $2`,
		"app", "u1",
	).Scan(&newID, &contextID, &taskID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if contextID != parent {
		t.Errorf("context_id = %q, want %q", contextID, parent)
	}
	if taskID != taskUUID {
		t.Errorf("task_id = %q, want %q", taskID, taskUUID)
	}

	var checkpointSessionID string
	if err := db.QueryRowContext(ctx,
		`SELECT session_id::text FROM task_checkpoints WHERE task_id = $1`,
		checkpointTaskID,
	).Scan(&checkpointSessionID); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if checkpointSessionID != newID {
		t.Errorf("checkpoint session_id = %q, want %q (lockstep with sessions row)",
			checkpointSessionID, newID)
	}
}

func assertMessageBackfillFidelity(t *testing.T, db *dbutil.DB, app, user string, want []Message) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT seq, message FROM session_messages WHERE app_name = ? AND user_id = ? ORDER BY seq`,
		app, user,
	)
	if err != nil {
		t.Fatalf("read session_messages: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []Message
	var seqs []int64
	for rows.Next() {
		var seq int64
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		var m Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("unmarshal message at seq %d: %v", seq, err)
		}
		seqs = append(seqs, seq)
		got = append(got, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("session_messages count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if seqs[i] != int64(i+1) {
			t.Errorf("row %d seq = %d, want %d (1-based, gap-free, array order)", i, seqs[i], i+1)
		}
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("row %d = {role:%q content:%q}, want {role:%q content:%q}",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

func backfillFidelityMessages() []Message {
	return []Message{
		{Role: "user", Content: "what is the capital of France?"},
		{Role: "assistant", Content: "The capital of France is Paris."},
		{Role: "user", Content: "and its population?"},
	}
}

func TestSessionBackfill_SQLite_NullMessagesBlob(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.MigrateUpTo(ctx, sessionUUIDVersion-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	uuid := "11111111-2222-7333-8444-777777777777"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, 0, 'null', '{}', '0', '0')`,
		"app", "u1", uuid,
	); err != nil {
		t.Fatalf("seed null-blob session: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_messages`).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 session_messages rows for a 'null' blob, got %d", count)
	}
}

func TestSessionBackfill_SQLite_MessageArrayFidelity(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.MigrateUpTo(ctx, sessionUUIDVersion-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	want := backfillFidelityMessages()
	blob, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}

	uuid := "11111111-2222-7333-8444-888888888888"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, '{}', '0', '0')`,
		"app", "u1", uuid, string(blob),
	); err != nil {
		t.Fatalf("seed multi-message session: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	assertMessageBackfillFidelity(t, db, "app", "u1", want)
}

func TestSessionBackfill_SQLite_DownRepackExecutes(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteSession(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	uuid := "11111111-2222-7333-8444-999999999999"
	if err := store.Create(ctx, &Session{AppName: "app", UserID: "u1", SessionID: uuid}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, r := range []struct {
		seq int
		msg string
	}{
		{3, `{"role":"user","content":"third"}`},
		{1, `{"role":"user","content":"first"}`},
		{2, `{"role":"assistant","content":"second"}`},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at)
			 VALUES (?, ?, ?, ?, ?, '0')`,
			"app", "u1", uuid, r.seq, r.msg,
		); err != nil {
			t.Fatalf("seed message seq %d: %v", r.seq, err)
		}
	}

	if err := store.MigrateDown(ctx); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
}

func TestSessionBackfill_Postgres_NullMessagesBlob(t *testing.T) {
	t.Parallel()
	tmpl := postgresTemplate(t)

	db, err := dbutil.Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(db)
	ctx := context.Background()

	uuid := "11111111-2222-7333-8444-777777777777"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, 0, 'null', '{}', '0', '0')`,
		"app", "u1", uuid,
	); err != nil {
		t.Fatalf("seed null-blob session: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_messages`).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 session_messages rows for a 'null' blob, got %d", count)
	}
}

func TestSessionBackfill_Postgres_MessageArrayFidelity(t *testing.T) {
	t.Parallel()
	tmpl := postgresTemplate(t)

	db, err := dbutil.Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(db)
	ctx := context.Background()

	want := backfillFidelityMessages()
	blob, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}

	uuid := "11111111-2222-7333-8444-888888888888"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (app_name, user_id, session_id, version, messages, metadata, created_at, updated_at)
		 VALUES ($1, $2, $3, 0, $4, '{}', '0', '0')`,
		"app", "u1", uuid, string(blob),
	); err != nil {
		t.Fatalf("seed multi-message session: %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	assertMessageBackfillFidelity(t, db, "app", "u1", want)
}
