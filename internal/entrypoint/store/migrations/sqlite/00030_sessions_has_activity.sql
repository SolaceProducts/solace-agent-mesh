-- +goose Up

ALTER TABLE sessions ADD COLUMN has_activity INTEGER NOT NULL DEFAULT 0;

UPDATE sessions SET has_activity = 1
WHERE id IN (SELECT session_id FROM chat_tasks)
   OR id IN (SELECT session_id FROM tasks WHERE session_id IS NOT NULL)
   OR source = 'scheduler';

ALTER TABLE sessions ADD COLUMN external_gateway INTEGER NOT NULL DEFAULT 0;

UPDATE sessions SET external_gateway = 1
WHERE id IN (SELECT session_id FROM gateway_session_keys WHERE gateway_type != 'scheduled');

CREATE INDEX idx_sessions_list_active
    ON sessions(user_id, updated_time DESC)
    WHERE deleted_at IS NULL AND has_activity = 1;

-- +goose Down

DROP INDEX IF EXISTS idx_sessions_list_active;
ALTER TABLE sessions DROP COLUMN external_gateway;
ALTER TABLE sessions DROP COLUMN has_activity;
