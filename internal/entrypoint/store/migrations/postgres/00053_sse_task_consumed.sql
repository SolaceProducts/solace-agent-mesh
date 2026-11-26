-- +goose Up

CREATE TABLE IF NOT EXISTS sse_task_consumed (
    task_id              UUID    PRIMARY KEY,
    consumed_through_seq INTEGER NOT NULL,
    consumed_at          BIGINT  NOT NULL
);

INSERT INTO gateway_async_indexes (name, definition, replaces, queued_at) VALUES
    ('idx_sse_event_buffer_task_consumed',
     'DROP INDEX CONCURRENTLY IF EXISTS idx_sse_event_buffer_task_consumed',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint),
    ('idx_sse_session_unconsumed',
     'DROP INDEX CONCURRENTLY IF EXISTS idx_sse_session_unconsumed',
     NULL,
     (EXTRACT(EPOCH FROM now()) * 1000)::bigint + 1)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DELETE FROM gateway_async_indexes
WHERE name IN ('idx_sse_event_buffer_task_consumed', 'idx_sse_session_unconsumed');

UPDATE sse_event_buffer s SET consumed = 1, consumed_at = c.consumed_at
FROM sse_task_consumed c
WHERE c.task_id = s.task_id AND s.event_sequence <= c.consumed_through_seq;
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_consumed ON sse_event_buffer(task_id, consumed);
CREATE INDEX IF NOT EXISTS idx_sse_session_unconsumed ON sse_event_buffer(session_id, consumed, event_sequence);
DROP TABLE IF EXISTS sse_task_consumed;
