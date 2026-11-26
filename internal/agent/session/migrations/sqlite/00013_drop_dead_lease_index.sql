-- +goose Up

DROP INDEX IF EXISTS idx_task_checkpoints_lease;

-- +goose Down

CREATE INDEX IF NOT EXISTS idx_task_checkpoints_lease
    ON task_checkpoints(lease_expires_at);
