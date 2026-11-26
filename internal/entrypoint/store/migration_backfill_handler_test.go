// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/google/uuid"
)

func toSeedUUID(label string) string {
	if _, err := uuid.Parse(label); err == nil {
		return label
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(label)).String() //nolint:forbidigo
}

const tasksHandlerBackfillVersionBefore = 31

type seededTask struct {
	id                string
	preSetHandlerName *string
	preSetHandlerType *string
	events            []seededEvent
	wantHandlerName   *string
	wantHandlerType   *string
	description       string
}

type seededEvent struct {
	id          string
	createdTime int64
	topic       string
	direction   string
	payload     string
}

func openFreshSQLiteForBackfill(t *testing.T, name string) *DB {
	t.Helper()
	dir := t.TempDir()
	url := "sqlite:///" + filepath.Join(dir, name+".db")
	db, err := Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open fresh sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedTasksAndEvents(t *testing.T, db *DB, tasks []seededTask) {
	t.Helper()
	ctx := context.Background()
	for _, task := range tasks {
		var nameCol, typeCol sql.NullString
		if task.preSetHandlerName != nil {
			nameCol = sql.NullString{String: *task.preSetHandlerName, Valid: true}
		}
		if task.preSetHandlerType != nil {
			typeCol = sql.NullString{String: *task.preSetHandlerType, Valid: true}
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO tasks (id, user_id, start_time)
			VALUES (?, ?, ?)`,
			toSeedUUID(task.id), "u1", int64(1),
		); err != nil {
			t.Fatalf("seed task %s: %v", task.id, err)
		}
		_ = nameCol
		_ = typeCol
		for _, e := range task.events {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO task_events (id, task_id, user_id, created_time, topic, direction, payload)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				toSeedUUID(e.id), toSeedUUID(task.id), "u1", e.createdTime, e.topic, e.direction, e.payload,
			); err != nil {
				t.Fatalf("seed event %s: %v", e.id, err)
			}
		}
	}
}

func applyPreSetHandlers(t *testing.T, db *DB, tasks []seededTask) {
	t.Helper()
	ctx := context.Background()
	for _, task := range tasks {
		if task.preSetHandlerName == nil && task.preSetHandlerType == nil {
			continue
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE tasks SET handler_name = ?, handler_type = ? WHERE id = ?`,
			task.preSetHandlerName, task.preSetHandlerType, toSeedUUID(task.id),
		); err != nil {
			t.Fatalf("pre-set handlers on %s: %v", task.id, err)
		}
	}
}

func assertBackfilledHandlers(t *testing.T, db *DB, tasks []seededTask) {
	t.Helper()
	ctx := context.Background()
	for _, task := range tasks {
		var gotName, gotType sql.NullString
		if err := db.QueryRowContext(ctx,
			`SELECT handler_name, handler_type FROM tasks WHERE id = ?`,
			toSeedUUID(task.id),
		).Scan(&gotName, &gotType); err != nil {
			t.Fatalf("read back task %s: %v", task.id, err)
		}
		wantName := ""
		wantNameValid := task.wantHandlerName != nil
		if wantNameValid {
			wantName = *task.wantHandlerName
		}
		if gotName.Valid != wantNameValid || gotName.String != wantName {
			t.Errorf("task %s (%s): handler_name = %+v, want valid=%v value=%q",
				task.id, task.description, gotName, wantNameValid, wantName)
		}
		wantType := ""
		wantTypeValid := task.wantHandlerType != nil
		if wantTypeValid {
			wantType = *task.wantHandlerType
		}
		if gotType.Valid != wantTypeValid || gotType.String != wantType {
			t.Errorf("task %s (%s): handler_type = %+v, want valid=%v value=%q",
				task.id, task.description, gotType, wantTypeValid, wantType)
		}
	}
}

func canonicalSeed() []seededTask {
	return []seededTask{
		{
			id:          "11111111-1111-4111-a111-111111111111",
			description: "agent — single /agent/request/ event, no workflow signal",
			events: []seededEvent{
				{
					id:          "e-a-1",
					createdTime: 100,
					topic:       "default/a2a/v1/agent/request/orchestrator",
					direction:   "request",
					payload:     `{"jsonrpc":"2.0","method":"tasks/send"}`,
				},
			},
			wantHandlerName: strPtr("orchestrator"),
			wantHandlerType: strPtr("agent"),
		},
		{
			id:          "22222222-2222-4222-a222-222222222222",
			description: "workflow — request event + workflow_execution_start signal",
			events: []seededEvent{
				{
					id:          "e-w-1",
					createdTime: 200,
					topic:       "default/a2a/v1/agent/request/MyWorkflow",
					direction:   "request",
					payload:     `{"jsonrpc":"2.0","method":"tasks/send"}`,
				},
				{
					id:          "e-w-2",
					createdTime: 201,
					topic:       "default/a2a/v1/agent/status/MyWorkflow/sub-1",
					direction:   "response",
					payload:     `{"type":"workflow_execution_start","workflow_name":"MyWorkflow"}`,
				},
			},
			wantHandlerName: strPtr("MyWorkflow"),
			wantHandlerType: strPtr("workflow"),
		},
		{
			id:          "33333333-3333-4333-a333-333333333333",
			description: "no /agent/request/ event — handler_name and _type stay NULL",
			events: []seededEvent{
				{
					id:          "e-n-1",
					createdTime: 300,
					topic:       "default/a2a/v1/gateway/status/gw-1/task-33333",
					direction:   "response",
					payload:     `{"kind":"status-update"}`,
				},
			},
			wantHandlerName: nil,
			wantHandlerType: nil,
		},
		{
			id:          "44444444-4444-4444-a444-444444444444",
			description: "earliest request wins — two request events, older topic name kept",
			events: []seededEvent{
				{
					id:          "e-e-2",
					createdTime: 402,
					topic:       "default/a2a/v1/agent/request/SecondAgent",
					direction:   "request",
					payload:     `{}`,
				},
				{
					id:          "e-e-1",
					createdTime: 401,
					topic:       "default/a2a/v1/agent/request/FirstAgent",
					direction:   "request",
					payload:     `{}`,
				},
			},
			wantHandlerName: strPtr("FirstAgent"),
			wantHandlerType: strPtr("agent"),
		},
		{
			id:                "55555555-5555-4555-a555-555555555555",
			description:       "idempotency — pre-set handler columns must not be overwritten",
			preSetHandlerName: strPtr("PreExisting"),
			preSetHandlerType: strPtr("agent"),
			events: []seededEvent{
				{
					id:          "e-i-1",
					createdTime: 500,
					topic:       "default/a2a/v1/agent/request/WouldOverwrite",
					direction:   "request",
					payload:     `{}`,
				},
			},
			wantHandlerName: strPtr("PreExisting"),
			wantHandlerType: strPtr("agent"),
		},
		{
			id:              "66666666-6666-4666-a666-666666666666",
			description:     "task with no events at all — no crash, no rows updated",
			events:          nil,
			wantHandlerName: nil,
			wantHandlerType: nil,
		},
		{
			id:          "77777777-7777-4777-a777-777777777777",
			description: "multi-segment topic tail — truncates to the first segment, matching classifyHandler",
			events: []seededEvent{
				{
					id:          "e-m-1",
					createdTime: 700,
					topic:       "default/a2a/v1/agent/request/SubAgent/extra",
					direction:   "request",
					payload:     `{}`,
				},
			},
			wantHandlerName: strPtr("SubAgent"),
			wantHandlerType: strPtr("agent"),
		},
		{
			id:          "88888888-8888-4888-a888-888888888888",
			description: "degenerate empty tail — handler_name/_type stay NULL, not empty string",
			events: []seededEvent{
				{
					id:          "e-z-1",
					createdTime: 800,
					topic:       "default/a2a/v1/agent/request/",
					direction:   "request",
					payload:     `{}`,
				},
			},
			wantHandlerName: nil,
			wantHandlerType: nil,
		},
		{
			id: "99999999-9999-4999-a999-999999999999",
			description: "workflow_<uuid> name with no workflow_execution_start signal — the prefix " +
				"alone must classify as workflow; a crashed workflow that never emitted the signal " +
				"would otherwise fall through to the payload-scan fallback and backfill as 'agent'",
			events: []seededEvent{
				{
					id:          "e-p-1",
					createdTime: 900,
					topic:       "default/a2a/v1/agent/request/workflow_99999999_9999_4999_a999_999999999999",
					direction:   "request",
					payload:     `{}`,
				},
			},
			wantHandlerName: strPtr("workflow_99999999_9999_4999_a999_999999999999"),
			wantHandlerType: strPtr("workflow"),
		},
		{
			id: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa",
			description: "agent_<uuid> name with an echoed workflow_execution_start signal in its own " +
				"event stream (PeerRouter forwards a delegated workflow's status onto the delegator's " +
				"task) — the prefix alone must classify as agent; without it, the payload-scan " +
				"fallback would backfill this as 'workflow'",
			events: []seededEvent{
				{
					id:          "e-q-1",
					createdTime: 1000,
					topic:       "default/a2a/v1/agent/request/agent_aaaaaaaa_aaaa_4aaa_aaaa_aaaaaaaaaaaa",
					direction:   "request",
					payload:     `{}`,
				},
				{
					id:          "e-q-2",
					createdTime: 1001,
					topic:       "default/a2a/v1/agent/status/agent_aaaaaaaa_aaaa_4aaa_aaaa_aaaaaaaaaaaa/sub-1",
					direction:   "response",
					payload:     `{"type":"workflow_execution_start","workflow_name":"DelegatedWorkflow"}`,
				},
			},
			wantHandlerName: strPtr("agent_aaaaaaaa_aaaa_4aaa_aaaa_aaaaaaaaaaaa"),
			wantHandlerType: strPtr("agent"),
		},
	}
}

func TestBackfill_SQLite_TasksHandlerClassification(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteForBackfill(t, "handler_backfill")
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, tasksHandlerBackfillVersionBefore); err != nil {
		t.Fatalf("migrate up to %d: %v", tasksHandlerBackfillVersionBefore, err)
	}

	seed := canonicalSeed()
	seedTasksAndEvents(t, db, seed)

	if err := db.MigrateUpTo(ctx, 32); err != nil {
		t.Fatalf("migrate up to 32: %v", err)
	}
	applyPreSetHandlers(t, db, seed)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations (runs backfill 33): %v", err)
	}

	assertBackfilledHandlers(t, db, seed)
}

func TestBackfill_SQLite_TasksHandlerClassification_ReRunIsIdempotent(t *testing.T) {
	t.Parallel()
	db := openFreshSQLiteForBackfill(t, "handler_backfill_rerun")
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, tasksHandlerBackfillVersionBefore); err != nil {
		t.Fatalf("migrate up to %d: %v", tasksHandlerBackfillVersionBefore, err)
	}
	seed := canonicalSeed()
	seedTasksAndEvents(t, db, seed)
	if err := db.MigrateUpTo(ctx, 32); err != nil {
		t.Fatalf("migrate up to 32: %v", err)
	}
	applyPreSetHandlers(t, db, seed)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("first backfill run: %v", err)
	}
	assertBackfilledHandlers(t, db, seed)

	if _, err := db.ExecContext(ctx, `
		WITH first_req AS (
			SELECT e.task_id,
				substr(e.topic, instr(e.topic, '/agent/request/') + 15) AS rest,
				ROW_NUMBER() OVER (PARTITION BY e.task_id ORDER BY e.created_time ASC, e.id ASC) AS rn
			FROM task_events e
			WHERE e.direction = 'request' AND e.topic LIKE '%/agent/request/%'
		)
		UPDATE tasks
		SET handler_name = (
			SELECT
				CASE
					WHEN fr.rest = '' THEN NULL
					WHEN instr(fr.rest, '/') > 0 THEN substr(fr.rest, 1, instr(fr.rest, '/') - 1)
					ELSE fr.rest
				END
			FROM first_req fr WHERE fr.task_id = tasks.id AND fr.rn = 1
		)
		WHERE handler_name IS NULL
		  AND EXISTS (SELECT 1 FROM first_req fr2 WHERE fr2.task_id = tasks.id AND fr2.rn = 1)
	`); err != nil {
		t.Fatalf("rerun step 1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE tasks SET handler_type = 'workflow'
		WHERE handler_type IS NULL AND handler_name LIKE 'workflow\_%' ESCAPE '\'
	`); err != nil {
		t.Fatalf("rerun step 2 (workflow prefix): %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE tasks SET handler_type = 'agent'
		WHERE handler_type IS NULL AND handler_name LIKE 'agent\_%' ESCAPE '\'
	`); err != nil {
		t.Fatalf("rerun step 2 (agent prefix): %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE tasks SET handler_type = 'workflow'
		WHERE handler_type IS NULL
		  AND handler_name IS NOT NULL
		  AND EXISTS (
			SELECT 1 FROM task_events te
			WHERE te.task_id = tasks.id AND te.payload LIKE '%workflow_execution_start%'
		)
	`); err != nil {
		t.Fatalf("rerun step 3: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE tasks SET handler_type = 'agent'
		WHERE handler_type IS NULL AND handler_name IS NOT NULL
	`); err != nil {
		t.Fatalf("rerun step 4: %v", err)
	}

	assertBackfilledHandlers(t, db, seed)
}

func TestBackfill_TasksHandler_DownIsNoOp(t *testing.T) {
	for _, tc := range []struct {
		dialect string
		fsys    fs.FS
	}{
		{"sqlite", sqliteMigrations()},
		{"postgres", postgresMigrations()},
	} {
		t.Run(tc.dialect, func(t *testing.T) {
			data, err := fs.ReadFile(tc.fsys, "00033_backfill_tasks_handler.sql")
			if err != nil {
				t.Fatalf("read migration file: %v", err)
			}
			idx := strings.Index(string(data), "-- +goose Down")
			if idx < 0 {
				t.Fatal("no '-- +goose Down' marker found")
			}
			down := strings.ToUpper(string(data)[idx:])
			if strings.Contains(down, "UPDATE") {
				t.Errorf("%s Down section contains an UPDATE — must be a no-op so a rollback of just\n"+
					"this migration can't null live handler_name/handler_type data:\n%s", tc.dialect, down)
			}
		})
	}
}

func TestBackfill_Postgres_TasksHandlerClassification(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}

	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_handler_backfill",
		func(u string) error {
			db, openErr := Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.MigrateUpTo(context.Background(), tasksHandlerBackfillVersionBefore)
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
	seed := canonicalSeed()
	seedTasksAndEvents(t, db, seed)

	if err := db.MigrateUpTo(ctx, 32); err != nil {
		t.Fatalf("migrate up to 32: %v", err)
	}
	applyPreSetHandlers(t, db, seed)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations (runs backfill 33): %v", err)
	}
	assertBackfilledHandlers(t, db, seed)
}
