-- +goose Up

ALTER TABLE task_pending_calls ADD COLUMN agent_name TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_task_pending_calls_agent_expiry
    ON task_pending_calls(agent_name, expires_at);

-- +goose Down

DROP INDEX IF EXISTS idx_task_pending_calls_agent_expiry;
ALTER TABLE task_pending_calls DROP COLUMN agent_name;
