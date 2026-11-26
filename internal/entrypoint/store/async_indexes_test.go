// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/pressly/goose/v3/lock"
)

const taskEventConfigDedupMigration = "task_event_config_dedup"

var wantTaskEventIndexDefs = map[string]string{
	"idx_task_events_task_time_id":       "CREATE INDEX idx_task_events_task_time_id ON public.task_events USING btree (task_id, created_time, id)",
	"idx_task_events_config_prompt_blob": "CREATE INDEX idx_task_events_config_prompt_blob ON public.task_events USING btree (config_prompt_blob) WHERE (config_prompt_blob IS NOT NULL)",
	"idx_task_events_config_tools_blob":  "CREATE INDEX idx_task_events_config_tools_blob ON public.task_events USING btree (config_tools_blob) WHERE (config_tools_blob IS NOT NULL)",
}

func openPreDedupPostgres(t *testing.T, name string) (*DB, string) {
	t.Helper()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	before := migrationVersion(t, postgresMigrations(), taskEventConfigDedupMigration) - 1
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_te_idx_"+name, func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(context.Background(), before)
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)
	url := tmpl.Clone(t)
	db, err := Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, url
}

func seedTaskEvents(t *testing.T, db *DB, taskID string, eventIDs ...string) {
	t.Helper()
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO tasks (id, user_id, start_time) VALUES (?, 'u1', 1)`, taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	for i, id := range eventIDs {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO task_events (id, task_id, user_id, created_time, topic, direction, payload)
			 VALUES (?, ?, 'u1', ?, 'ns/a2a/v1/agent/request/a', 'request', '{}')`,
			id, taskID, i+1); err != nil {
			t.Fatalf("seed event %s: %v", id, err)
		}
	}
}

func taskEventIndexDefs(t *testing.T, db *DB) map[string]string {
	t.Helper()
	rows, err := db.RawDB().QueryContext(t.Context(),
		`SELECT indexname, indexdef FROM pg_indexes WHERE tablename = 'task_events'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	defs := map[string]string{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		defs[name] = def
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return defs
}

const taskEventIndexOIDSum = `SELECT sum(indexrelid::oid::int8) FROM pg_index WHERE indrelid = 'task_events'::regclass`

func taskEventIndexOIDs(t *testing.T, db *DB) int64 {
	t.Helper()
	var sum int64
	if err := db.RawDB().QueryRowContext(t.Context(), taskEventIndexOIDSum).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	return sum
}

func taskEventIndexOIDsByName(t *testing.T, db *DB) map[string]int64 {
	t.Helper()
	rows, err := db.RawDB().QueryContext(t.Context(),
		`SELECT c.relname, c.oid::int8 FROM pg_index i
		 JOIN pg_class c ON c.oid = i.indexrelid WHERE i.indrelid = 'task_events'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	oids := map[string]int64{}
	for rows.Next() {
		var name string
		var oid int64
		if err := rows.Scan(&name, &oid); err != nil {
			t.Fatal(err)
		}
		oids[name] = oid
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return oids
}

func holdAsyncIndexLock(t *testing.T, url string) {
	t.Helper()
	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	conn, err := holderDB.RawDB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	lockID := dbutil.AsyncIndexLockID(holderDB.asyncIndexConfig().QueueTable)
	if _, err := conn.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncIndexes_UpgradeBuildsTaskEventIndexesAfterStartup_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "upgrade")
	seedTaskEvents(t, db, "10000000-0000-0000-0000-000000000001",
		"20000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002")

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defs := taskEventIndexDefs(t, db)
	for name := range wantTaskEventIndexDefs {
		if _, built := defs[name]; built {
			t.Errorf("migrations built %s; a table scan does not belong before /health is served", name)
		}
	}
	if _, kept := defs["idx_task_events_task_id"]; !kept {
		t.Error("migrations dropped idx_task_events_task_id before its replacement exists")
	}
	ready, err := NewTaskEventRepo(db).Blobs().ReferenceIndexesReady(ctx)
	if err != nil || ready {
		t.Fatalf("ReferenceIndexesReady before the async builds = %v, %v; want false", ready, err)
	}

	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build async indexes: %v", err)
	}
	defs = taskEventIndexDefs(t, db)
	for name, want := range wantTaskEventIndexDefs {
		if defs[name] != want {
			t.Errorf("%s = %q, want %q", name, defs[name], want)
		}
	}
	if _, stale := defs["idx_task_events_task_id"]; stale {
		t.Error("idx_task_events_task_id survived although idx_task_events_task_time_id covers it")
	}
	if ready, err := NewTaskEventRepo(db).Blobs().ReferenceIndexesReady(ctx); err != nil || !ready {
		t.Fatalf("ReferenceIndexesReady after the async builds = %v, %v; want true", ready, err)
	}

	if unbuilt := unbuiltAsyncIndexes(t, db); unbuilt != 0 {
		t.Errorf("%d queued indexes still unbuilt after a successful pass", unbuilt)
	}

	before := taskEventIndexOIDs(t, db)
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("second async index pass: %v", err)
	}
	if taskEventIndexOIDs(t, db) != before {
		t.Error("a second pass rebuilt indexes that were already valid")
	}
}

func TestAsyncIndexes_KeepIndexesTheReleasedMigrationsBuilt_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "released")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, def := range wantTaskEventIndexDefs {
		if _, err := db.RawDB().ExecContext(ctx, def); err != nil {
			t.Fatalf("recreate released index: %v", err)
		}
	}
	if _, err := db.RawDB().ExecContext(ctx, `DROP INDEX idx_task_events_task_id`); err != nil {
		t.Fatal(err)
	}
	before := taskEventIndexOIDsByName(t, db)

	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build async indexes: %v", err)
	}
	after := taskEventIndexOIDsByName(t, db)
	for name, oid := range before {
		if after[name] != oid {
			t.Errorf("%s was rebuilt or dropped (oid %d -> %d); it already existed before the async builds", name, oid, after[name])
		}
	}
	if _, recreated := after["idx_task_events_task_id"]; recreated {
		t.Error("the async builds re-created idx_task_events_task_id, which idx_task_events_task_time_id replaces")
	}
}

func TestAsyncIndexes_RebuildIndexLeftInvalid_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "invalid")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	seedTaskEvents(t, db, "10000000-0000-0000-0000-000000000002",
		"20000000-0000-0000-0000-000000000003", "20000000-0000-0000-0000-000000000004")

	if _, err := db.RawDB().ExecContext(ctx,
		`CREATE UNIQUE INDEX CONCURRENTLY idx_task_events_config_prompt_blob ON task_events (user_id)`); err == nil {
		t.Fatal("seeding an invalid index: the unique build unexpectedly succeeded")
	}
	if exists, valid, err := dbutil.PostgresIndexState(ctx, db.RawDB(), "idx_task_events_config_prompt_blob"); err != nil || !exists || valid {
		t.Fatalf("seeded index state exists=%v valid=%v err=%v; want an invalid index", exists, valid, err)
	}

	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build async indexes: %v", err)
	}
	if exists, valid, err := dbutil.PostgresIndexState(ctx, db.RawDB(), "idx_task_events_config_prompt_blob"); err != nil || !exists || !valid {
		t.Fatalf("rebuilt index state exists=%v valid=%v err=%v; want valid", exists, valid, err)
	}
	if got, want := taskEventIndexDefs(t, db)["idx_task_events_config_prompt_blob"], wantTaskEventIndexDefs["idx_task_events_config_prompt_blob"]; got != want {
		t.Errorf("rebuilt idx_task_events_config_prompt_blob = %q, want %q", got, want)
	}
}

func queueAsyncIndex(t *testing.T, db *DB, name, definition string) {
	t.Helper()
	queueAsyncIndexAt(t, db, name, definition, time.Now().UnixMilli())
}

func queueAsyncIndexAt(t *testing.T, db *DB, name, definition string, queuedAt int64) {
	t.Helper()
	if _, err := db.RawDB().ExecContext(t.Context(),
		`INSERT INTO gateway_async_indexes (name, definition, queued_at) VALUES ($1, $2, $3)
		 ON CONFLICT (name) DO UPDATE SET definition = EXCLUDED.definition, replaces = EXCLUDED.replaces,
		   queued_at = EXCLUDED.queued_at, completed_at = NULL, attempts = 0, next_attempt_at = NULL, last_error = NULL`,
		name, definition, queuedAt); err != nil {
		t.Fatal(err)
	}
}

func unbuiltAsyncIndexes(t *testing.T, db *DB) int {
	t.Helper()
	var unbuilt int
	if err := db.RawDB().QueryRowContext(t.Context(),
		`SELECT count(*) FROM gateway_async_indexes WHERE completed_at IS NULL`).Scan(&unbuilt); err != nil {
		t.Fatal(err)
	}
	return unbuilt
}

func showStatementTimeout(t *testing.T, db *DB) string {
	t.Helper()
	var timeout string
	if err := db.RawDB().QueryRowContext(t.Context(), `SHOW statement_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	return timeout
}

type asyncIndexRow struct {
	attempts      int
	lastError     *string
	completedAt   *int64
	nextAttemptAt *int64
}

func readAsyncIndexRow(t *testing.T, db *DB, name string) asyncIndexRow {
	t.Helper()
	var r asyncIndexRow
	if err := db.RawDB().QueryRowContext(t.Context(),
		`SELECT attempts, last_error, completed_at, next_attempt_at FROM gateway_async_indexes WHERE name = $1`, name).
		Scan(&r.attempts, &r.lastError, &r.completedAt, &r.nextAttemptAt); err != nil {
		t.Fatal(err)
	}
	return r
}

func runAsAsyncBuilder(t *testing.T, url, name, statement string) <-chan error {
	t.Helper()
	ctx := t.Context()
	builderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = builderDB.Close() })
	conn, err := builderDB.RawDB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	var pid int
	if err := conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx,
		`UPDATE gateway_async_indexes SET builder_pid = pg_backend_pid(), started_at = $1 WHERE name = $2`,
		time.Now().UnixMilli(), name); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := conn.ExecContext(ctx, statement)
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var active bool
		if err := builderDB.RawDB().QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND state = 'active' AND query = $2)`,
			pid, statement).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend %d never showed %q running", pid, statement)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func alterTaskEventsMigration(column string) fstest.MapFS {
	return fstest.MapFS{"00001_alter.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\nALTER TABLE task_events ADD COLUMN " + column + " TEXT;\n" +
			"-- +goose Down\nALTER TABLE task_events DROP COLUMN " + column + ";\n")}}
}

func taskEventsHasColumn(t *testing.T, db *DB, column string) bool {
	t.Helper()
	var has bool
	if err := db.RawDB().QueryRowContext(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'task_events' AND column_name = $1)`, column).Scan(&has); err != nil {
		t.Fatal(err)
	}
	return has
}

func TestMigrate_CancelsRunningAsyncIndexBuildOnLockTimeout_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "cancel")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	eventIDs := make([]string, 30)
	for i := range eventIDs {
		eventIDs[i] = fmt.Sprintf("2000000c-0000-0000-0000-%012d", i+1)
	}
	seedTaskEvents(t, db, "1000000c-0000-0000-0000-000000000001", eventIDs...)
	if _, err := db.RawDB().ExecContext(ctx,
		`CREATE FUNCTION slow_index_key(text) RETURNS text LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN PERFORM pg_sleep(1); RETURN $1; END $$`); err != nil {
		t.Fatal(err)
	}
	const name = "idx_task_events_slow_probe"
	build := `CREATE INDEX CONCURRENTLY IF NOT EXISTS ` + name + ` ON task_events (slow_index_key(id::text))`
	queueAsyncIndex(t, db, name, build)
	buildErr := runAsAsyncBuilder(t, url, name, build)

	cfg := db.migrateConfig()
	cfg.TableName = "cancel_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("cancel_probe")
	cfg.LockTimeout = 2 * time.Second
	start := time.Now()
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate behind a running async build = %v; want the build cancelled and the migration applied", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("migrate took %s; want one lock timeout and a retry, not the whole build", elapsed)
	}
	select {
	case err := <-buildErr:
		if err == nil || !strings.Contains(err.Error(), "canceling statement due to user request") {
			t.Fatalf("the running build ended with %v; want it cancelled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the running build was not cancelled")
	}
	if !taskEventsHasColumn(t, db, "cancel_probe") {
		t.Fatal("the migration was not applied after the build was cancelled")
	}
	if exists, valid, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || (exists && valid) {
		t.Errorf("%s exists=%v valid=%v err=%v after the cancel; want absent or INVALID for the next pass to rebuild", name, exists, valid, err)
	}
}

func queueSlowTaskEventsBuild(t *testing.T, db *DB, name string, rows int) {
	t.Helper()
	ctx := t.Context()
	eventIDs := make([]string, rows)
	for i := range eventIDs {
		eventIDs[i] = fmt.Sprintf("2000000d-0000-0000-0000-%012d", i+1)
	}
	seedTaskEvents(t, db, "1000000d-0000-0000-0000-000000000001", eventIDs...)
	if _, err := db.RawDB().ExecContext(ctx,
		`CREATE FUNCTION slow_index_key(text) RETURNS text LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN PERFORM pg_sleep(1); RETURN $1; END $$`); err != nil {
		t.Fatal(err)
	}
	queueAsyncIndex(t, db, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (slow_index_key(id::text))`)
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestMigrate_LogsAMigrationStillRunning_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "progress")
	var first syncBuffer
	cfg := db.migrateConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(&first, nil))
	cfg.TableName = "progress_probe_goose_version"
	cfg.ProgressInterval = 200 * time.Millisecond
	cfg.PostgresFS = fstest.MapFS{"00001_slow.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\nSELECT pg_sleep(1);\n-- +goose Down\nSELECT 1;\n")}}
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	out := first.String()
	for _, want := range []string{"applying pending migrations", "migration still running", "pg_sleep", "versions=[1]", "state=active", "applied migration", "migrations applied"} {
		if !strings.Contains(out, want) {
			t.Errorf("migration logs lack %q:\n%s", want, out)
		}
	}

	var second syncBuffer
	cfg.Logger = slog.New(slog.NewTextHandler(&second, nil))
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if out := second.String(); !strings.Contains(out, "migrations up to date") || !strings.Contains(out, "version=1") {
		t.Errorf("a run with nothing pending did not log the version it is at:\n%s", out)
	}
}

func TestAsyncIndexes_LogsBuildProgress_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "buildprogress")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_progress_probe"
	queueSlowTaskEventsBuild(t, db, name, 3)
	var logs syncBuffer
	cfg := db.asyncIndexConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	cfg.ProgressInterval = 300 * time.Millisecond
	if err := dbutil.BuildAsyncIndexes(ctx, cfg); err != nil {
		t.Fatalf("build: %v", err)
	}
	out := logs.String()
	for _, want := range []string{"async index operations pending", "async index operation in progress", "index=" + name, "phase=", "built async index"} {
		if !strings.Contains(out, want) {
			t.Errorf("build logs lack %q:\n%s", want, out)
		}
	}

	var drained syncBuffer
	cfg.Logger = slog.New(slog.NewTextHandler(&drained, nil))
	queueAsyncIndex(t, db, "idx_task_events_drain_probe", `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_drain_probe ON task_events (task_id)`)
	dbutil.RunAsyncIndexes(ctx, cfg)
	if out := drained.String(); !strings.Contains(out, "async index queue drained") || !strings.Contains(out, "completed=1") {
		t.Errorf("RunAsyncIndexes did not log the queue draining:\n%s", out)
	}
}

func TestMigrate_CancelledBuildIsRecordedAndRebuilt_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "cancelbuilder")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_builder_probe"
	queueSlowTaskEventsBuild(t, db, name, 8)

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); db.RunAsyncIndexes(runCtx) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var active bool
		if err := db.RawDB().QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE state = 'active' AND query ILIKE 'CREATE INDEX CONCURRENTLY%' AND query LIKE '%'||$1||'%')`, name).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the builder never started the slow build")
		}
		time.Sleep(50 * time.Millisecond)
	}

	cfg := db.migrateConfig()
	cfg.TableName = "builder_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("builder_probe")
	cfg.LockTimeout = time.Second
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate behind the builder = %v; want the build cancelled and the migration applied", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for r := readAsyncIndexRow(t, db, name); r.lastError == nil; r = readAsyncIndexRow(t, db, name) {
		if time.Now().After(deadline) {
			t.Fatal("the builder never recorded the cancel")
		}
		time.Sleep(100 * time.Millisecond)
	}
	r := readAsyncIndexRow(t, db, name)
	if r.attempts != 0 {
		t.Errorf("a cancel counted as a failed attempt: attempts = %d, want 0", r.attempts)
	}
	var builderPid *int
	if err := db.RawDB().QueryRowContext(ctx, `SELECT builder_pid FROM gateway_async_indexes WHERE name = $1`, name).Scan(&builderPid); err != nil {
		t.Fatal(err)
	}
	if builderPid != nil || r.nextAttemptAt == nil || r.lastError == nil || !strings.Contains(*r.lastError, "canceling statement") {
		t.Fatalf("row after the cancel: builderPid=%v nextAttemptAt=%v lastError=%v; want the pid cleared, a backoff and the cancel recorded", builderPid, r.nextAttemptAt, r.lastError)
	}
	select {
	case <-done:
		t.Fatal("RunAsyncIndexes exited after the cancel instead of waiting out the backoff")
	case <-time.After(time.Second):
	}

	clearAsyncIndexBackoff(t, db, name)
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("rebuild after the cancel: %v", err)
	}
	if exists, valid, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || !exists || !valid {
		t.Fatalf("%s exists=%v valid=%v err=%v after the rebuild; want valid", name, exists, valid, err)
	}
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAsyncIndexes did not stop when its context was cancelled")
	}
}

func TestMigrate_LeavesOtherSessionsAloneOnLockTimeout_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "nocancel")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_idle_probe"
	queueAsyncIndex(t, db, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (note)`)
	sleepErr := runAsAsyncBuilder(t, url, name, `SELECT pg_sleep(5)`)

	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	holder, err := holderDB.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err = holder.ExecContext(ctx, `SELECT count(*) FROM task_events`); err != nil {
		t.Fatal(err)
	}

	cfg := db.migrateConfig()
	cfg.TableName = "nocancel_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("nocancel_probe")
	cfg.LockTimeout = time.Second
	err = dbutil.Migrate(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("migrate behind a reader with no build running = %v; want a lock timeout error", err)
	}
	select {
	case err := <-sleepErr:
		if err != nil {
			t.Fatalf("the session that took over the builder's pid was cancelled: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("pg_sleep(5) did not finish")
	}
	if taskEventsHasColumn(t, db, "nocancel_probe") {
		t.Fatal("the migration was applied although its lock was never released")
	}
}

func waitForRunningBuild(t *testing.T, db *DB, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var active bool
		if err := db.RawDB().QueryRowContext(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE state = 'active' AND query ILIKE 'CREATE INDEX CONCURRENTLY%' AND query LIKE '%'||$1||'%')`, name).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no session started the build of %s", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMigrate_RetryAfterCancelBeatsTheNextQueuedBuild_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "cancelnext")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const first, second = "idx_task_events_first_probe", "idx_task_events_second_probe"
	queueSlowTaskEventsBuild(t, db, first, 8)
	queueAsyncIndexAt(t, db, second,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS `+second+` ON task_events (slow_index_key(id::text))`, time.Now().UnixMilli()+1)

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); otherGWE.RunAsyncIndexes(runCtx) }()
	waitForRunningBuild(t, db, first)

	cfg := db.migrateConfig()
	cfg.TableName = "cancelnext_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("cancelnext_probe")
	cfg.LockTimeout = time.Second
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate while another GWE builds two queued indexes = %v; want the first build cancelled and the migration applied", err)
	}
	if !taskEventsHasColumn(t, db, "cancelnext_probe") {
		t.Fatal("the migration was not applied")
	}
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunAsyncIndexes did not stop when its context was cancelled")
	}
	if r := readAsyncIndexRow(t, db, first); r.attempts != 0 || r.lastError == nil {
		t.Errorf("cancelled row attempts=%d lastError=%v; want the cancel recorded without counting an attempt", r.attempts, r.lastError)
	}
	var secondTouched bool
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT started_at IS NOT NULL OR builder_pid IS NOT NULL FROM gateway_async_indexes WHERE name = $1`, second).Scan(&secondTouched); err != nil {
		t.Fatal(err)
	}
	if secondTouched {
		t.Error("the builder started the next queued build after the cancel; want the pass ended so the migration's retry gets the table")
	}
}

func TestMigrate_LockTimeoutOnAnotherTableLeavesTheBuildRunning_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "othertable")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_other_table_probe"
	queueSlowTaskEventsBuild(t, db, name, 3)
	buildErr := runAsAsyncBuilder(t, url, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (slow_index_key(id::text))`)

	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	holder, err := holderDB.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err = holder.ExecContext(ctx, `SELECT count(*) FROM feedback`); err != nil {
		t.Fatal(err)
	}

	cfg := db.migrateConfig()
	cfg.TableName = "othertable_probe_goose_version"
	cfg.PostgresFS = fstest.MapFS{"00001_alter.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\nALTER TABLE feedback ADD COLUMN othertable_probe TEXT;\n" +
			"-- +goose Down\nALTER TABLE feedback DROP COLUMN othertable_probe;\n")}}
	cfg.LockTimeout = time.Second
	err = dbutil.Migrate(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("migrate behind a transaction on feedback = %v; want a lock timeout error", err)
	}
	if err = holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-buildErr:
		if err != nil {
			t.Fatalf("the build on task_events ended with %v; want it left to finish", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the build on task_events did not finish")
	}
}

func TestAsyncIndexes_RequeueDuringABuildStaysPending_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "requeue")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_requeue_probe"
	queueSlowTaskEventsBuild(t, db, name, 3)

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	buildErr := make(chan error, 1)
	go func() { buildErr <- otherGWE.BuildAsyncIndexes(ctx) }()
	waitForRunningBuild(t, db, name)
	queueAsyncIndex(t, db, name, `DROP INDEX CONCURRENTLY IF EXISTS `+name)

	select {
	case err := <-buildErr:
		if err != nil {
			t.Fatalf("the old build: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the old build did not finish")
	}
	if r := readAsyncIndexRow(t, db, name); r.completedAt != nil || r.attempts != 0 {
		t.Fatalf("re-queued row after the old build ended: completedAt=%v attempts=%d; want it pending", r.completedAt, r.attempts)
	}
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("run the re-queued drop: %v", err)
	}
	if exists, _, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || exists {
		t.Fatalf("%s exists=%v err=%v; want the re-queued drop to have run", name, exists, err)
	}
}

func TestAsyncIndexes_RequeueOfALaterRowInThePassRunsTheNewDefinition_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "requeuelater")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const first, later = "idx_task_events_first_in_pass_probe", "idx_task_events_redefined_probe"
	queueSlowTaskEventsBuild(t, db, first, 3)
	queueAsyncIndexAt(t, db, later, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+later+` ON task_events (task_id)`, time.Now().UnixMilli()+1)

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	passErr := make(chan error, 1)
	go func() { passErr <- otherGWE.BuildAsyncIndexes(ctx) }()
	waitForRunningBuild(t, db, first)
	queueAsyncIndexAt(t, db, later, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+later+` ON task_events (created_time)`, time.Now().UnixMilli()+2)

	select {
	case err = <-passErr:
		if err != nil {
			t.Fatalf("the pass that read the old definition: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the pass that read the old definition did not finish")
	}
	if err = db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("run the re-queued definition: %v", err)
	}
	var def string
	if err = db.RawDB().QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = $1`, later).Scan(&def); err != nil {
		t.Fatalf("read %s: %v", later, err)
	}
	if !strings.Contains(def, "(created_time)") {
		t.Fatalf("%s is %q; want the re-queued definition on created_time", later, def)
	}
}

func TestAsyncIndexes_RequeueDuringItsOperationKeepsTheBuilderRunning_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "requeueduring")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_requeue_during_probe"
	queueSlowTaskEventsBuild(t, db, name, 3)

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	var logs syncBuffer
	cfg := otherGWE.asyncIndexConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); dbutil.RunAsyncIndexes(runCtx, cfg) }()
	waitForRunningBuild(t, db, name)
	queueAsyncIndex(t, db, name, `DROP INDEX CONCURRENTLY IF EXISTS `+name)

	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(logs.String(), "re-queued during its operation") {
		if time.Now().After(deadline) {
			t.Fatalf("the old build never finished and found its row re-queued:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatalf("RunAsyncIndexes stopped with the re-queued operation still pending:\n%s", logs.String())
	case <-time.After(3 * time.Second):
	}
	for line := range strings.Lines(logs.String()) {
		if strings.Contains(line, "index="+name) && strings.Contains(line, `msg="built async index"`) || strings.Contains(line, "async index queue drained") {
			t.Errorf("the builder reported the re-queued row as built: %s", line)
		}
	}
	if r := readAsyncIndexRow(t, db, name); r.completedAt != nil {
		t.Errorf("re-queued row completedAt=%v; want it pending for the next pass", r.completedAt)
	}
}

func TestMigrate_LockTimeoutCancelsOnlyWhatBlockedTheFailedStatement_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "segmentblockers")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_segment_probe"
	queueSlowTaskEventsBuild(t, db, name, 12)
	build := `CREATE INDEX CONCURRENTLY IF NOT EXISTS ` + name + ` ON task_events (slow_index_key(id::text))`

	builderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = builderDB.Close() })
	builder, err := builderDB.RawDB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = builder.Close() })
	if _, err = builder.ExecContext(ctx,
		`UPDATE gateway_async_indexes SET builder_pid = pg_backend_pid(), started_at = $1 WHERE name = $2`, time.Now().UnixMilli(), name); err != nil {
		t.Fatal(err)
	}
	var builderPID int
	if err = builder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&builderPID); err != nil {
		t.Fatal(err)
	}
	earlier, err := builder.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = earlier.ExecContext(ctx, `LOCK TABLE task_events IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	buildErr := make(chan error, 1)
	go func() {
		if waitErr := waitForSessionBlockedBy(ctx, db.RawDB(), builderPID); waitErr != nil {
			buildErr <- waitErr
			return
		}
		time.Sleep(1500 * time.Millisecond)
		if commitErr := earlier.Commit(); commitErr != nil {
			buildErr <- commitErr
			return
		}
		_, execErr := builder.ExecContext(ctx, build)
		buildErr <- execErr
	}()

	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	holder, err := holderDB.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err = holder.ExecContext(ctx, `SELECT count(*) FROM feedback`); err != nil {
		t.Fatal(err)
	}

	cfg := db.migrateConfig()
	cfg.TableName = "segmentblockers_probe_goose_version"
	cfg.PostgresFS = fstest.MapFS{
		"00001_alter_task_events.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\nALTER TABLE task_events ADD COLUMN segment_probe TEXT;\n" +
				"-- +goose Down\nALTER TABLE task_events DROP COLUMN segment_probe;\n")},
		"00002_alter_feedback.sql": &fstest.MapFile{Data: []byte(
			"-- +goose Up\nALTER TABLE feedback ADD COLUMN segment_probe TEXT;\n" +
				"-- +goose Down\nALTER TABLE feedback DROP COLUMN segment_probe;\n")},
	}
	cfg.LockTimeout = 5 * time.Second
	err = dbutil.Migrate(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("migrate with the second migration behind a transaction on feedback = %v; want a lock timeout error", err)
	}
	if !taskEventsHasColumn(t, db, "segment_probe") {
		t.Fatal("the first migration, which waited out the builder's lock, was not applied")
	}
	if err = holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-buildErr:
		if err != nil {
			t.Fatalf("the builder's later build ended with %v; want it left to finish", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the builder's later build did not finish")
	}
}

func waitForSessionBlockedBy(ctx context.Context, db *sql.DB, pid int) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		var blocked bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			return err
		}
		if blocked {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no session was blocked by backend %d", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAsyncIndexes_DeadlockWithAMigrationIsNotAFailedAttempt_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "deadlock")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_deadlock_probe"
	queueSlowTaskEventsBuild(t, db, name, 4)

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); otherGWE.RunAsyncIndexes(runCtx) }()
	waitForRunningBuild(t, db, name)

	cfg := db.migrateConfig()
	cfg.TableName = "deadlock_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("deadlock_probe")
	cfg.LockTimeout = 30 * time.Second
	if err = dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate behind a build near its end = %v; want it applied once Postgres resolves the deadlock", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	r := readAsyncIndexRow(t, db, name)
	for r.lastError == nil {
		if time.Now().After(deadline) {
			t.Fatal("the builder never recorded the interrupted build")
		}
		time.Sleep(100 * time.Millisecond)
		r = readAsyncIndexRow(t, db, name)
	}
	if !strings.Contains(*r.lastError, "deadlock detected") {
		t.Fatalf("recorded %q; want the build aborted by a deadlock with the migration", *r.lastError)
	}
	if r.attempts != 0 {
		t.Errorf("a deadlock with a migration counted as a failed attempt: attempts = %d, want 0", r.attempts)
	}
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunAsyncIndexes did not stop when its context was cancelled")
	}
}

func TestMigrate_MigrationThatLosesADeadlockToABuildIsRetried_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "deadlockretry")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var dbName string
	if err := db.RawDB().QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	quotedDBName := `"` + strings.ReplaceAll(dbName, `"`, `""`) + `"`
	if _, err := db.RawDB().ExecContext(ctx, `ALTER DATABASE `+quotedDBName+` SET deadlock_timeout = '5s'`); err != nil {
		t.Fatal(err)
	}
	const name = "idx_task_events_deadlockretry_probe"
	queueSlowTaskEventsBuild(t, db, name, 2)

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	migrator, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrator.Close() })
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); otherGWE.RunAsyncIndexes(runCtx) }()
	waitForRunningBuild(t, db, name)

	var logs syncBuffer
	cfg := migrator.migrateConfig()
	cfg.TableName = "deadlockretry_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("deadlockretry_probe")
	cfg.LockTimeout = 30 * time.Second
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	if err = dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate aborted by a deadlock with a build = %v; want it retried and applied:\n%s", err, logs.String())
	}
	if !taskEventsHasColumn(t, db, "deadlockretry_probe") {
		t.Fatal("the migration was not applied after its deadlock retry")
	}
	if out := logs.String(); !strings.Contains(out, "migration aborted by a deadlock; retrying it once") {
		t.Errorf("migration logs lack the deadlock retry; want the migration to have been the deadlock victim:\n%s", out)
	}
	deadline := time.Now().Add(30 * time.Second)
	r := readAsyncIndexRow(t, db, name)
	for r.completedAt == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the build never completed after the migration lost the deadlock: attempts=%d lastError=%v", r.attempts, r.lastError)
		}
		time.Sleep(100 * time.Millisecond)
		r = readAsyncIndexRow(t, db, name)
	}
	if r.attempts != 0 {
		t.Errorf("the build that won the deadlock recorded attempts = %d, want 0", r.attempts)
	}
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunAsyncIndexes did not stop when its context was cancelled")
	}
}

func TestMigrate_LockTimeoutCancelsTheDropOfAnInvalidIndex_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "predrop")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build the migrations' queued indexes: %v", err)
	}
	seedTaskEvents(t, db, "1000000e-0000-0000-0000-000000000001",
		"2000000e-0000-0000-0000-000000000001", "2000000e-0000-0000-0000-000000000002")
	const name = "idx_task_events_predrop_probe"
	if _, err := db.RawDB().ExecContext(ctx,
		`CREATE UNIQUE INDEX CONCURRENTLY `+name+` ON task_events (user_id)`); err == nil {
		t.Fatal("seeding an invalid index: the unique build unexpectedly succeeded")
	}
	queueAsyncIndex(t, db, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (user_id)`)

	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	holder, err := holderDB.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Rollback() })
	if _, err = holder.ExecContext(ctx, `SELECT count(*) FROM task_events`); err != nil {
		t.Fatal(err)
	}

	otherGWE, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = otherGWE.Close() })
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() { defer close(done); otherGWE.RunAsyncIndexes(runCtx) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var dropping bool
		if err = db.RawDB().QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE state = 'active' AND query ILIKE 'DROP INDEX CONCURRENTLY%' AND query LIKE '%'||$1||'%')`, name).Scan(&dropping); err != nil {
			t.Fatal(err)
		}
		if dropping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the builder never started dropping the invalid %s", name)
		}
		time.Sleep(50 * time.Millisecond)
	}

	var logs syncBuffer
	cfg := db.migrateConfig()
	cfg.TableName = "predrop_probe_goose_version"
	cfg.PostgresFS = fstest.MapFS{"00001_stats.sql": &fstest.MapFile{Data: []byte(
		"-- +goose Up\nALTER TABLE task_events ALTER COLUMN user_id SET STATISTICS 200;\n" +
			"-- +goose Down\nALTER TABLE task_events ALTER COLUMN user_id SET STATISTICS -1;\n")}}
	cfg.LockTimeout = 3 * time.Second
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	if err = dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate behind the drop of an invalid index = %v; want the drop cancelled and the migration applied", err)
	}
	if !strings.Contains(logs.String(), "cancelled a running async index build so a migration can take its lock") {
		t.Errorf("the migration applied without cancelling the drop; logs:\n%s", logs.String())
	}
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunAsyncIndexes did not stop when its context was cancelled")
	}
}

func TestMigrate_WaitingForTheMigrationLockNamesItsHolder_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "lockholder")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	holder, err := holderDB.RawDB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	var holderPID int
	if err = holder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err = holder.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}

	var logs syncBuffer
	cfg := db.migrateConfig()
	cfg.TableName = "lockholder_probe_goose_version"
	cfg.PostgresFS = alterTaskEventsMigration("lockholder_probe")
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	cfg.ProgressInterval = 200 * time.Millisecond
	migrateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = dbutil.Migrate(migrateCtx, cfg); err == nil {
		t.Fatal("migrate while another session holds the migration lock succeeded")
	}
	out := logs.String()
	for _, want := range []string{"migration still running", "activity=\"waiting for the migration lock\"", "lockHolder=", "PID:" + strconv.Itoa(holderPID) + " "} {
		if !strings.Contains(out, want) {
			t.Errorf("migration logs lack %q while another session holds the migration lock:\n%s", want, out)
		}
	}
	if strings.Contains(out, "statement=") {
		t.Errorf("migration logs report the lock holder's statement as the migration's own:\n%s", out)
	}
}

func clearAsyncIndexBackoff(t *testing.T, db *DB, name string) {
	t.Helper()
	if _, err := db.RawDB().ExecContext(t.Context(),
		`UPDATE gateway_async_indexes SET next_attempt_at = NULL WHERE name = $1`, name); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncIndexes_RecordFailedBuildAndRetry_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "failed")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_note"
	queueAsyncIndex(t, db, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (note)`)

	if err := db.BuildAsyncIndexes(ctx); err == nil {
		t.Fatal("a build over a missing column succeeded")
	}
	if r := readAsyncIndexRow(t, db, name); r.attempts != 1 || r.lastError == nil || *r.lastError == "" || r.completedAt != nil {
		t.Fatalf("failed build recorded attempts=%d lastError=%v completedAt=%v; want 1, an error, pending", r.attempts, r.lastError, r.completedAt)
	}
	if r := readAsyncIndexRow(t, db, name); r.nextAttemptAt == nil || *r.nextAttemptAt <= time.Now().UnixMilli() {
		t.Fatalf("failed build recorded nextAttemptAt=%v; want a time in the future", r.nextAttemptAt)
	}
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("pass while the row waits out its backoff = %v; want it skipped", err)
	}
	if r := readAsyncIndexRow(t, db, name); r.attempts != 1 {
		t.Fatalf("a pass inside the backoff retried the build: attempts = %d, want 1", r.attempts)
	}
	clearAsyncIndexBackoff(t, db, name)

	if _, err := db.RawDB().ExecContext(ctx, `ALTER TABLE task_events ADD COLUMN note TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("retry once the build can succeed: %v", err)
	}
	if r := readAsyncIndexRow(t, db, name); r.completedAt == nil || r.lastError != nil {
		t.Errorf("retried build left completedAt=%v lastError=%v; want completed with no error", r.completedAt, r.lastError)
	}
	if exists, valid, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || !exists || !valid {
		t.Errorf("retried index exists=%v valid=%v err=%v; want valid", exists, valid, err)
	}
}

func TestAsyncIndexes_RefuseUniqueIndex_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "unique")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_unique_id"
	queueAsyncIndex(t, db, name, `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (id)`)

	if err := db.BuildAsyncIndexes(ctx); err == nil {
		t.Fatal("a queued unique index was built")
	}
	if exists, _, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || exists {
		t.Errorf("unique index exists=%v err=%v; want it refused", exists, err)
	}
	if r := readAsyncIndexRow(t, db, name); r.completedAt != nil || r.lastError == nil {
		t.Errorf("refused unique index recorded completedAt=%v lastError=%v; want pending with an error", r.completedAt, r.lastError)
	}
}

func TestAsyncIndexes_DropIndex_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "drop")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build the queued indexes: %v", err)
	}
	const name = "idx_task_events_config_tools_blob"
	if exists, valid, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || !exists || !valid {
		t.Fatalf("%s exists=%v valid=%v err=%v after the queued build; want valid", name, exists, valid, err)
	}
	queueAsyncIndex(t, db, name, `DROP INDEX CONCURRENTLY IF EXISTS `+name)

	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("run the queued drop: %v", err)
	}
	if exists, _, err := dbutil.PostgresIndexState(ctx, db.RawDB(), name); err != nil || exists {
		t.Errorf("%s exists=%v err=%v after a queued drop", name, exists, err)
	}
	if r := readAsyncIndexRow(t, db, name); r.completedAt == nil || r.attempts != 0 || r.lastError != nil {
		t.Errorf("queued drop recorded attempts=%d lastError=%v completedAt=%v; want completed with no error", r.attempts, r.lastError, r.completedAt)
	}
}

func TestAsyncIndexes_FailingIndexDoesNotBlockLaterOnes_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "blocked")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_aaa_missing"
	queueAsyncIndexAt(t, db, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (no_such_column)`, 0)

	err := db.BuildAsyncIndexes(ctx)
	if err == nil || !strings.Contains(err.Error(), name) {
		t.Fatalf("async index pass with a failing first index = %v; want an error naming %s", err, name)
	}
	if r := readAsyncIndexRow(t, db, name); r.attempts != 1 || r.lastError == nil || *r.lastError == "" || r.completedAt != nil {
		t.Errorf("failed build recorded attempts=%d lastError=%v completedAt=%v; want 1, an error, pending", r.attempts, r.lastError, r.completedAt)
	}
	if ready, err := NewTaskEventRepo(db).Blobs().ReferenceIndexesReady(ctx); err != nil || !ready {
		t.Errorf("ReferenceIndexesReady behind a failing index = %v, %v; want true", ready, err)
	}
	if unbuilt := unbuiltAsyncIndexes(t, db); unbuilt != 1 {
		t.Errorf("%d queued indexes unbuilt; want only the failing one", unbuilt)
	}
}

func TestAsyncIndexes_RestoreStatementTimeoutAfterBuilds_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "timeout")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var dbName string
	if err := db.RawDB().QueryRowContext(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RawDB().ExecContext(ctx, `ALTER DATABASE "`+dbName+`" SET statement_timeout = '5min'`); err != nil {
		t.Fatal(err)
	}
	builder, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = builder.Close() })
	builder.RawDB().SetMaxOpenConns(1)
	before := showStatementTimeout(t, builder)
	if before == "0" {
		t.Fatal("the database default statement_timeout did not apply")
	}

	if err := builder.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("build async indexes: %v", err)
	}
	if unbuilt := unbuiltAsyncIndexes(t, builder); unbuilt != 0 {
		t.Fatalf("%d queued indexes still unbuilt after a successful pass", unbuilt)
	}
	if after := showStatementTimeout(t, builder); after != before {
		t.Errorf("statement_timeout after the builds = %q, want %q", after, before)
	}
}

func TestAsyncIndexes_RunReturnsOnceEverythingIsBuilt_Postgres(t *testing.T) {
	t.Parallel()
	db, _ := openPreDedupPostgres(t, "run")
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	db.RunAsyncIndexes(ctx)
	if ctx.Err() != nil {
		t.Fatalf("RunAsyncIndexes returned only when its context ended: %v", ctx.Err())
	}
	if unbuilt := unbuiltAsyncIndexes(t, db); unbuilt != 0 {
		t.Errorf("%d queued indexes still unbuilt after RunAsyncIndexes returned", unbuilt)
	}
	if ready, err := NewTaskEventRepo(db).Blobs().ReferenceIndexesReady(t.Context()); err != nil || !ready {
		t.Errorf("ReferenceIndexesReady after RunAsyncIndexes = %v, %v; want true", ready, err)
	}
}

func TestAsyncIndexes_RunStopsOnCancelWhileAnotherProcessHoldsThem_Postgres(t *testing.T) {
	t.Parallel()
	db, url := openPreDedupPostgres(t, "runbusy")
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	holdAsyncIndexLock(t, url)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		db.RunAsyncIndexes(ctx)
	}()
	select {
	case <-done:
		t.Fatal("RunAsyncIndexes returned while another process held the lock and indexes were pending")
	case <-time.After(500 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAsyncIndexes did not return within 5s of its context being cancelled")
	}
	if unbuilt := unbuiltAsyncIndexes(t, db); unbuilt == 0 {
		t.Error("indexes were built while another process held the lock")
	}
}

func TestAsyncIndexes_WaitWhileAnotherProcessHoldsThem_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "busy")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	holdAsyncIndexLock(t, url)

	err := db.BuildAsyncIndexes(ctx)
	if !errors.Is(err, dbutil.ErrAsyncIndexesBusy) {
		t.Fatalf("async index pass while another process holds the lock = %v; want ErrAsyncIndexesBusy", err)
	}
	if exists, _, err := dbutil.PostgresIndexState(ctx, db.RawDB(), "idx_task_events_task_time_id"); err != nil || exists {
		t.Errorf("idx_task_events_task_time_id exists=%v err=%v while another process held the lock; want nothing built", exists, err)
	}
	if timeout := showStatementTimeout(t, db); timeout != "0" {
		t.Errorf("statement_timeout leaked into the pool as %q", timeout)
	}
}

func TestAsyncIndexes_GiveUpAfterRepeatedFailures_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "giveup")
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	const name = "idx_task_events_never"
	queueAsyncIndex(t, db, name, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+name+` ON task_events (no_such_column)`)
	if _, err := db.RawDB().ExecContext(ctx,
		`UPDATE gateway_async_indexes SET attempts = $1 WHERE name = $2`, dbutil.MaxAsyncIndexAttempts-1, name); err != nil {
		t.Fatal(err)
	}

	if err := db.BuildAsyncIndexes(ctx); err == nil {
		t.Fatal("the last allowed attempt succeeded over a missing column")
	}
	if r := readAsyncIndexRow(t, db, name); r.attempts != dbutil.MaxAsyncIndexAttempts || r.completedAt != nil {
		t.Fatalf("after the last allowed attempt: attempts=%d completedAt=%v; want %d and pending", r.attempts, r.completedAt, dbutil.MaxAsyncIndexAttempts)
	}
	clearAsyncIndexBackoff(t, db, name)
	if err := db.BuildAsyncIndexes(ctx); err != nil {
		t.Fatalf("pass after giving up = %v; want the row left alone", err)
	}
	if r := readAsyncIndexRow(t, db, name); r.attempts != dbutil.MaxAsyncIndexAttempts {
		t.Errorf("a pass after giving up retried the build: attempts = %d", r.attempts)
	}

	var logs syncBuffer
	cfg := db.asyncIndexConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dbutil.RunAsyncIndexes(runCtx, cfg)
	if runCtx.Err() != nil {
		t.Fatal("RunAsyncIndexes kept retrying a row that gave up")
	}
	if unbuilt := unbuiltAsyncIndexes(t, db); unbuilt != 1 {
		t.Errorf("%d queued indexes unbuilt; want only the row that gave up, left for an operator", unbuilt)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "async index builds gave up") || !strings.Contains(out, name) {
		t.Errorf("a restarted builder did not warn about the row that gave up:\n%s", out)
	}
}

func TestMigrate_NotBlockedByAsyncIndexBuilds_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "startup")
	holdAsyncIndexLock(t, url)

	migrateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.Migrate(migrateCtx); err != nil {
		t.Fatalf("migrations while an async index build holds its lock: %v", err)
	}
}

func TestMigrate_LockHeldOnTaskEventsFailsFast_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, url := openPreDedupPostgres(t, "lock")
	db.RawDB().SetMaxOpenConns(2)

	holderDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holderDB.Close() })
	holder, err := holderDB.RawDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = holder.ExecContext(ctx, `SELECT count(*) FROM task_events`); err != nil {
		t.Fatal(err)
	}
	var holderPID int
	if err = holder.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}

	var logs syncBuffer
	cfg := db.migrateConfig()
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	cfg.LockTimeout = 2 * time.Second
	cfg.ProgressInterval = 200 * time.Millisecond
	start := time.Now()
	err = dbutil.Migrate(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("migrate behind a reader holding task_events = %v; want a lock timeout error", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("migrate took %s to give up; want it bounded by the lock timeout", elapsed)
	}
	for _, want := range []string{"migration still running", "waitEventType=Lock", "blockedBy=", "PID:" + strconv.Itoa(holderPID) + " ", "State:idle in transaction"} {
		if out := logs.String(); !strings.Contains(out, want) {
			t.Errorf("migration logs lack %q while a reader holds task_events:\n%s", want, out)
		}
	}
	if out := logs.String(); strings.Contains(out, "count(*) FROM task_events") {
		t.Errorf("migration logs carry the blocking session's statement text, which may hold literals from any client:\n%s", out)
	}
	assertNoLockTimeoutLeak(t, db.RawDB(), "after the lock timeout")

	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate once the reader is gone: %v", err)
	}
	assertNoLockTimeoutLeak(t, db.RawDB(), "after a successful run")
}

func assertNoLockTimeoutLeak(t *testing.T, db *sql.DB, when string) {
	t.Helper()
	ctx := t.Context()
	var pinned []*sql.Conn
	defer func() {
		for _, conn := range pinned {
			_ = conn.Close()
		}
	}()
	for i := range 2 {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		pinned = append(pinned, conn)
		var setting string
		if err := conn.QueryRowContext(ctx, `SHOW lock_timeout`).Scan(&setting); err != nil {
			t.Fatal(err)
		}
		if setting != "0" {
			t.Errorf("lock_timeout leaked into pooled connection %d %s as %q", i, when, setting)
		}
	}
}

func TestMigrate_NoLockTimeoutForConcurrentIndexBuilds_Postgres(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, _ := openPreDedupPostgres(t, "cic_lock")
	const assertLockTimeout = `-- +goose StatementBegin
DO $$ BEGIN
  IF current_setting('lock_timeout') %s '0' THEN RAISE EXCEPTION 'lock_timeout is %%', current_setting('lock_timeout'); END IF;
END $$;
-- +goose StatementEnd
`
	migrations := fstest.MapFS{
		"00001_plain.sql": &fstest.MapFile{Data: []byte("-- +goose Up\n" +
			fmt.Sprintf(assertLockTimeout, "=") +
			"CREATE TABLE lock_probe (id INTEGER);\n-- +goose Down\nDROP TABLE lock_probe;\n")},
	}
	cfg := db.migrateConfig()
	cfg.TableName = "lock_probe_goose_version"
	cfg.PostgresFS = migrations
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("a transactional migration ran without lock_timeout: %v", err)
	}

	migrations["00002_concurrent.sql"] = &fstest.MapFile{Data: []byte("-- +goose NO TRANSACTION\n-- +goose Up\n" +
		fmt.Sprintf(assertLockTimeout, "<>") +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_lock_probe ON lock_probe (id);\n-- +goose Down\nDROP INDEX CONCURRENTLY IF EXISTS idx_lock_probe;\n")}
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("a CONCURRENTLY migration ran with lock_timeout set: %v", err)
	}

	migrations["00003_plain.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\n" +
		fmt.Sprintf(assertLockTimeout, "=") +
		"ALTER TABLE lock_probe ADD COLUMN note TEXT;\n-- +goose Down\nALTER TABLE lock_probe DROP COLUMN note;\n")}
	migrations["00004_concurrent.sql"] = &fstest.MapFile{Data: []byte("-- +goose NO TRANSACTION\n-- +goose Up\n" +
		fmt.Sprintf(assertLockTimeout, "<>") +
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_lock_probe_note ON lock_probe (note);\n-- +goose Down\nDROP INDEX CONCURRENTLY IF EXISTS idx_lock_probe_note;\n")}
	migrations["00005_plain.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\n" +
		fmt.Sprintf(assertLockTimeout, "=") +
		"ALTER TABLE lock_probe ADD COLUMN tag TEXT;\n-- +goose Down\nALTER TABLE lock_probe DROP COLUMN tag;\n")}
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("one run mixing transactional and CONCURRENTLY migrations: %v", err)
	}
}
