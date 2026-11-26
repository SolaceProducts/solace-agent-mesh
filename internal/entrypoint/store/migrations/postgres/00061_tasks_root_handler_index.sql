-- +goose Up

INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_tasks_root_handler_starttime',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_root_handler_starttime ON tasks (handler_type, handler_name, start_time DESC) WHERE parent_task_id IS NULL',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DELETE FROM gateway_async_indexes WHERE name = 'idx_tasks_root_handler_starttime';
DROP INDEX IF EXISTS idx_tasks_root_handler_starttime;
