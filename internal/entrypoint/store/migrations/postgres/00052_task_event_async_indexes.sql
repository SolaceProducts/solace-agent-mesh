-- +goose Up

CREATE TABLE IF NOT EXISTS gateway_async_indexes (
    name            TEXT PRIMARY KEY,
    definition      TEXT NOT NULL,
    replaces        TEXT,
    queued_at       BIGINT NOT NULL,
    completed_at    BIGINT,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at BIGINT,
    builder_pid     INTEGER,
    started_at      BIGINT,
    last_error      TEXT
);

INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_task_events_config_prompt_blob',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_config_prompt_blob ON task_events (config_prompt_blob) WHERE config_prompt_blob IS NOT NULL',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint),
    ('idx_task_events_config_tools_blob',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_config_tools_blob ON task_events (config_tools_blob) WHERE config_tools_blob IS NOT NULL',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint + 1),
    ('idx_task_events_task_time_id',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_task_time_id ON task_events (task_id, created_time, id)',
     'idx_task_events_task_id',
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint + 2)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS gateway_async_indexes;
