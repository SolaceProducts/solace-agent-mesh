-- +goose Up

CREATE TABLE IF NOT EXISTS task_pending_calls (
    corr_id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_task_pending_calls_task ON task_pending_calls(task_id);
CREATE INDEX IF NOT EXISTS idx_task_pending_calls_expiry ON task_pending_calls(expires_at);

ALTER TABLE task_checkpoints ADD COLUMN claimed_at TEXT;

-- +goose Down

ALTER TABLE task_checkpoints DROP COLUMN claimed_at;
DROP TABLE IF EXISTS task_pending_calls;
