-- +goose Up

CREATE TABLE IF NOT EXISTS task_event_blobs (
    hash       TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    content    TEXT NOT NULL,
    byte_len   INTEGER NOT NULL,
    first_seen BIGINT NOT NULL,
    last_seen  BIGINT NOT NULL
);

ALTER TABLE task_events ADD COLUMN IF NOT EXISTS signal_type  TEXT;
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS agent_name   TEXT;
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS tool_name    TEXT;
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS event_seq    BIGINT;
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS publisher_id TEXT;
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS config_prompt_blob TEXT;
ALTER TABLE task_events ADD COLUMN IF NOT EXISTS config_tools_blob TEXT;

ALTER TABLE task_events ADD COLUMN IF NOT EXISTS commit_xid xid8;

ALTER TABLE task_events SET (autovacuum_vacuum_scale_factor = 0.02, toast.autovacuum_vacuum_scale_factor = 0.02);

-- +goose Down

ALTER TABLE task_events RESET (autovacuum_vacuum_scale_factor, toast.autovacuum_vacuum_scale_factor);
DROP INDEX IF EXISTS idx_task_events_task_time_id;
CREATE INDEX IF NOT EXISTS idx_task_events_task_id ON task_events (task_id);
DROP INDEX IF EXISTS idx_task_events_commit_xid;
ALTER TABLE task_events DROP COLUMN IF EXISTS commit_xid;
ALTER TABLE task_events DROP COLUMN IF EXISTS config_tools_blob;
ALTER TABLE task_events DROP COLUMN IF EXISTS config_prompt_blob;
ALTER TABLE task_events DROP COLUMN IF EXISTS publisher_id;
ALTER TABLE task_events DROP COLUMN IF EXISTS event_seq;
ALTER TABLE task_events DROP COLUMN IF EXISTS tool_name;
ALTER TABLE task_events DROP COLUMN IF EXISTS agent_name;
ALTER TABLE task_events DROP COLUMN IF EXISTS signal_type;
DROP TABLE IF EXISTS task_event_blobs;
