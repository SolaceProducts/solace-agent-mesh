-- +goose Up

INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_feedback_created_at',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_feedback_created_at ON feedback (created_at)',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint),
    ('idx_tasks_session_roots',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_session_roots ON tasks (session_id, start_time DESC) WHERE parent_task_id IS NULL',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DELETE FROM gateway_async_indexes WHERE name IN ('idx_feedback_created_at', 'idx_tasks_session_roots');
DROP INDEX IF EXISTS idx_feedback_created_at;
DROP INDEX IF EXISTS idx_tasks_session_roots;
