// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrationsafety

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "released 00045 built task_events indexes inline",
			sql: `-- +goose Up
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS commit_xid xid8;
CREATE INDEX IF NOT EXISTS idx_task_events_commit_xid ON task_events (commit_xid) WHERE commit_xid IS NOT NULL;
DROP INDEX IF EXISTS idx_task_events_task_id;
CREATE INDEX IF NOT EXISTS idx_task_events_task_time_id ON task_events (task_id, created_time, id);
-- +goose Down
CREATE INDEX IF NOT EXISTS idx_task_events_task_id ON task_events (task_id);`,
			want: []string{"task_events: building an index", "task_events: building an index"},
		},
		{
			name: "released 00047 built task_events indexes concurrently",
			sql: `-- +goose NO TRANSACTION
-- +goose Up
DROP INDEX CONCURRENTLY IF EXISTS idx_task_events_config_prompt_blob;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_config_prompt_blob ON task_events (config_prompt_blob) WHERE config_prompt_blob IS NOT NULL;`,
			want: []string{"even CONCURRENTLY"},
		},
		{
			name: "catalog-only changes to task_events",
			sql: `-- +goose Up
CREATE TABLE IF NOT EXISTS task_event_blobs (hash TEXT PRIMARY KEY, content TEXT NOT NULL);
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS signal_type TEXT;
ALTER TABLE task_events ADD COLUMN retries INTEGER NOT NULL DEFAULT 0;
ALTER TABLE task_events ADD COLUMN seen_at TIMESTAMPTZ DEFAULT now();
ALTER TABLE task_events SET (autovacuum_vacuum_scale_factor = 0.02);
ALTER TABLE task_events DROP COLUMN IF EXISTS legacy;
DROP INDEX IF EXISTS idx_task_events_old;`,
		},
		{
			name: "queueing task_events indexes for a build after startup",
			sql: `-- +goose Up
CREATE TABLE IF NOT EXISTS gateway_async_indexes (name TEXT PRIMARY KEY, definition TEXT NOT NULL, replaces TEXT, queued_at BIGINT NOT NULL);
INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_task_events_task_time_id',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_task_time_id ON task_events (task_id, created_time, id)',
     'idx_task_events_task_id', 1),
    ('idx_task_events_commit_xid',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_commit_xid ON task_events (commit_xid) WHERE commit_xid IS NOT NULL',
     NULL, 1),
    ('idx_task_events_legacy', 'DROP INDEX CONCURRENTLY IF EXISTS idx_task_events_legacy', NULL, 1)
ON CONFLICT (name) DO NOTHING;`,
		},
		{
			name: "queued definitions the builder would refuse",
			sql: `-- +goose Up
INSERT INTO gateway_async_indexes (name, definition, queued_at) VALUES
    ('idx_a', 'CREATE INDEX idx_a ON task_events (a)', 1),
    ('idx_b', 'CREATE INDEX CONCURRENTLY idx_b ON task_events (b)', 1),
    ('idx_c', 'CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_c ON task_events (c)', 1),
    ('idx_d', 'DROP INDEX idx_d', 1);`,
			want: []string{"CONCURRENTLY IF NOT EXISTS", "CONCURRENTLY IF NOT EXISTS", "unique index cannot be queued", "DROP INDEX CONCURRENTLY IF EXISTS"},
		},
		{
			name: "queued names the builder would refuse",
			sql: `-- +goose Up
INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_task_events_a', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_a ON task_events (a)', 'public.idx_task_events_old', 1),
    ('Idx_Task_Events_B', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS Idx_Task_Events_B ON task_events (b)', NULL, 1);`,
			want: []string{"plain lowercase index names", "plain lowercase index names"},
		},
		{
			name: "queued definitions for an index other than the row's",
			sql: `-- +goose Up
INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_task_events_a', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_b ON task_events (b)', 'idx_task_events_old', 1),
    ('idx_task_events_c', 'DROP INDEX CONCURRENTLY IF EXISTS idx_task_events_old', NULL, 1);`,
			want: []string{"names idx_task_events_b but the row is named idx_task_events_a", "names idx_task_events_old but the row is named idx_task_events_c"},
		},
		{
			name: "a schema-qualified queue is checked like any other",
			sql: `-- +goose Up
INSERT INTO evidence.evidence_async_indexes (name, definition, queued_at) VALUES
    ('traces_outcome_idx', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS traces_outcome_idx ON evidence.traces (outcome)', 1),
    ('traces_old_idx', 'DROP INDEX CONCURRENTLY IF EXISTS traces_old_idx', 2),
    ('traces_agent_idx', 'CREATE INDEX traces_agent_idx ON evidence.traces (agent_name)', 3),
    ('traces_root_idx', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS traces_root_idx ON traces (root_task_id)', 4),
    ('traces_node_idx', 'CREATE INDEX CONCURRENTLY IF NOT EXISTS traces_node_idx ON public.traces (node_id)', 5);`,
			want: []string{"CONCURRENTLY IF NOT EXISTS", "must name its table as evidence.<table>", "must name its table as evidence.<table>"},
		},
		{
			name: "a growing table created in the same migration may be indexed and filled",
			sql: `-- +goose Up
CREATE TABLE IF NOT EXISTS task_records (id TEXT PRIMARY KEY, last_seen_at BIGINT);
CREATE INDEX IF NOT EXISTS idx_task_records_last_seen_at ON task_records (last_seen_at);
INSERT INTO task_records (id, last_seen_at) VALUES ('a', 1);`,
		},
		{
			name: "tables that stay small are not checked",
			sql: `-- +goose Up
CREATE INDEX IF NOT EXISTS idx_agents_name ON agents (name);
UPDATE agents SET enabled = true WHERE enabled IS NULL;
ALTER TABLE rbac_roles ALTER COLUMN id TYPE UUID USING id::uuid;`,
		},
		{
			name: "comments and the Down section are not checked",
			sql: `-- +goose Up
-- CREATE INDEX idx ON task_events (x); UPDATE tasks SET x = 1;
/* DELETE FROM tasks; */
-- the sweep's check isn't a string
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS note TEXT;
-- +goose Down
UPDATE tasks SET note = NULL;
CREATE INDEX idx_tasks_note ON tasks (note);`,
		},
		{
			name: "backfill of a growing table",
			sql: `-- +goose Up
UPDATE feedback SET details_submitted_time = updated_at WHERE details_submitted_time IS NULL;`,
			want: []string{"feedback: UPDATE touches every matching row"},
		},
		{
			name: "batched backfill inside a DO block",
			sql: `-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE n INTEGER;
BEGIN
  LOOP
    UPDATE tasks SET status = 'done' WHERE id IN (SELECT id FROM tasks WHERE status IS NULL LIMIT 1000 FOR UPDATE SKIP LOCKED);
    GET DIAGNOSTICS n = ROW_COUNT;
    EXIT WHEN n = 0;
  END LOOP;
END $$;
-- +goose StatementEnd`,
			want: []string{"tasks: UPDATE touches every matching row"},
		},
		{
			name: "backfill in a WITH query",
			sql: `-- +goose Up
WITH b AS (SELECT id FROM task_events LIMIT 10 FOR UPDATE SKIP LOCKED) UPDATE task_events SET x = 1 WHERE id IN (SELECT id FROM b);`,
			want: []string{"task_events: UPDATE touches every matching row", "task_events: a WITH query reads"},
		},
		{
			name: "a WITH query copying a growing table elsewhere",
			sql: `-- +goose Up
WITH src AS (SELECT id FROM task_events) INSERT INTO task_archive SELECT id FROM src;`,
			want: []string{"task_events: a WITH query reads"},
		},
		{
			name: "a function's FROM before the source table",
			sql: `-- +goose Up
INSERT INTO task_snapshot (id, snapshot_at) SELECT id, (EXTRACT(EPOCH FROM now()) * 1000)::bigint FROM tasks;
CREATE TABLE task_copy AS SELECT id, EXTRACT(EPOCH FROM now()) AS t FROM sessions s JOIN task_events e ON e.task_id = s.id;`,
			want: []string{"tasks: INSERT ... SELECT", "sessions: CREATE TABLE ... AS", "task_events: CREATE TABLE ... AS"},
		},
		{
			name: "tables and materialized views filled from a growing table",
			sql: `-- +goose Up
CREATE TABLE IF NOT EXISTS task_archive AS SELECT * FROM public."task_events" WHERE created_time < 1;
CREATE MATERIALIZED VIEW IF NOT EXISTS task_counts AS SELECT user_id, count(*) FROM tasks GROUP BY user_id;`,
			want: []string{"task_events: CREATE TABLE ... AS", "tasks: CREATE MATERIALIZED VIEW"},
		},
		{
			name: "a materialized view created WITH NO DATA",
			sql: `-- +goose Up
CREATE MATERIALIZED VIEW task_counts AS SELECT user_id, count(*) FROM tasks GROUP BY user_id WITH NO DATA;`,
		},
		{
			name: "conditional ADD COLUMN inside a DO block",
			sql: `-- +goose Up
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'tasks' AND column_name = 'note') THEN
    ALTER TABLE tasks ADD COLUMN note TEXT;
  END IF;
END $$;`,
		},
		{
			name: "rewrites and scans of a growing table",
			sql: `-- +goose Up
ALTER TABLE tasks ALTER COLUMN id TYPE UUID USING id::uuid;
ALTER TABLE tasks ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE tasks ADD COLUMN token UUID DEFAULT gen_random_uuid();
ALTER TABLE tasks ADD COLUMN seq BIGSERIAL;
ALTER TABLE tasks ADD CONSTRAINT tasks_user_fk FOREIGN KEY (user_id) REFERENCES users (id);
ALTER TABLE tasks VALIDATE CONSTRAINT tasks_user_fk;
ALTER TABLE tasks ADD CONSTRAINT tasks_key UNIQUE (user_id, start_time);
DELETE FROM sse_event_buffer WHERE created_time < 1;
INSERT INTO task_archive SELECT * FROM task_events;
VACUUM FULL tasks;`,
			want: []string{
				"changing a column type",
				"SET NOT NULL",
				"volatile default",
				"serial column",
				"validated against every row",
				"VALIDATE CONSTRAINT",
				"UNIQUE, PRIMARY KEY or EXCLUDE",
				"sse_event_buffer: DELETE",
				"task_events: INSERT ... SELECT",
				"VACUUM FULL",
			},
		},
		{
			name: "constraints added NOT VALID",
			sql: `-- +goose Up
ALTER TABLE tasks ADD CONSTRAINT tasks_user_fk FOREIGN KEY (user_id) REFERENCES users (id) NOT VALID;
ALTER TABLE tasks ADD CONSTRAINT tasks_user_nn CHECK (user_id IS NOT NULL) NOT VALID;`,
		},
		{
			name: "schema-qualified and quoted names",
			sql: `-- +goose Up
CREATE INDEX "idx_x" ON public."task_events" (topic);`,
			want: []string{"task_events: building an index"},
		},
		{
			name: "lowercase Up annotation",
			sql: `-- +goose up
UPDATE tasks SET note = NULL;
-- +goose down
UPDATE tasks SET note = 'x';`,
			want: []string{"tasks: UPDATE"},
		},
		{
			name: "Up annotation without a space after the dashes",
			sql: `--+goose Up
UPDATE tasks SET note = NULL;
--+goose Down
UPDATE tasks SET note = 'x';`,
			want: []string{"tasks: UPDATE"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFindings(t, Check([]byte(tc.sql)), tc.want)
		})
	}
}

func assertFindings(t *testing.T, got []Finding, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d findings, want %d:\n%s", len(got), len(want), formatFindings(got))
	}
	for i, w := range want {
		if !strings.Contains(got[i].Reason, w) {
			t.Errorf("finding %d = %q, want it to mention %q", i, got[i].Reason, w)
		}
	}
}

func formatFindings(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("  ")
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}
