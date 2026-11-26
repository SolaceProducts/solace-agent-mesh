-- +goose Up

INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_sched_exec_unfinalized',
     'CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sched_exec_unfinalized ON scheduled_task_executions (scheduled_for) WHERE finalized_at IS NULL',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DELETE FROM gateway_async_indexes WHERE name = 'idx_sched_exec_unfinalized';
DROP INDEX IF EXISTS idx_sched_exec_unfinalized;
