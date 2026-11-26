-- +goose Up

INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_task_events_nondelta_id',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_task_events_nondelta_id ON task_events (id) WHERE is_delta = 0',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DROP INDEX IF EXISTS idx_task_events_nondelta_id;
DELETE FROM gateway_async_indexes WHERE name = 'idx_task_events_nondelta_id';
