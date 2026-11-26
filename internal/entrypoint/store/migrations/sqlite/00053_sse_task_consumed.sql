-- +goose Up

CREATE TABLE IF NOT EXISTS sse_task_consumed (
    task_id              TEXT    PRIMARY KEY,
    consumed_through_seq INTEGER NOT NULL,
    consumed_at          INTEGER NOT NULL
);

DROP INDEX IF EXISTS idx_sse_event_buffer_task_consumed;
DROP INDEX IF EXISTS idx_sse_session_unconsumed;

-- +goose Down

UPDATE sse_event_buffer SET consumed = 1, consumed_at = (
    SELECT c.consumed_at FROM sse_task_consumed c WHERE c.task_id = sse_event_buffer.task_id
)
WHERE event_sequence <= (
    SELECT c.consumed_through_seq FROM sse_task_consumed c WHERE c.task_id = sse_event_buffer.task_id
);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_consumed ON sse_event_buffer(task_id, consumed);
CREATE INDEX IF NOT EXISTS idx_sse_session_unconsumed ON sse_event_buffer(session_id, consumed, event_sequence);
DROP TABLE IF EXISTS sse_task_consumed;
