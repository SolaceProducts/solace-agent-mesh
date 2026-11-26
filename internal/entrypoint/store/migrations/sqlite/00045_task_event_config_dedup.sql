-- +goose Up

CREATE TABLE IF NOT EXISTS task_event_blobs (
    hash       TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    content    TEXT NOT NULL,
    byte_len   INTEGER NOT NULL,
    first_seen INTEGER NOT NULL,
    last_seen  INTEGER NOT NULL
);

ALTER TABLE task_events ADD COLUMN signal_type  TEXT;
ALTER TABLE task_events ADD COLUMN agent_name   TEXT;
ALTER TABLE task_events ADD COLUMN tool_name    TEXT;
ALTER TABLE task_events ADD COLUMN event_seq    INTEGER;
ALTER TABLE task_events ADD COLUMN publisher_id TEXT;
ALTER TABLE task_events ADD COLUMN config_prompt_blob TEXT;
ALTER TABLE task_events ADD COLUMN config_tools_blob TEXT;

ALTER TABLE task_events ADD COLUMN commit_xid INTEGER;

DROP INDEX IF EXISTS idx_task_events_task_id;
CREATE INDEX IF NOT EXISTS idx_task_events_task_time_id ON task_events (task_id, created_time, id);

-- +goose Down

DROP INDEX IF EXISTS idx_task_events_task_time_id;
CREATE INDEX IF NOT EXISTS idx_task_events_task_id ON task_events (task_id);
ALTER TABLE task_events DROP COLUMN commit_xid;
ALTER TABLE task_events DROP COLUMN config_tools_blob;
ALTER TABLE task_events DROP COLUMN config_prompt_blob;
ALTER TABLE task_events DROP COLUMN publisher_id;
ALTER TABLE task_events DROP COLUMN event_seq;
ALTER TABLE task_events DROP COLUMN tool_name;
ALTER TABLE task_events DROP COLUMN agent_name;
ALTER TABLE task_events DROP COLUMN signal_type;
DROP TABLE IF EXISTS task_event_blobs;
