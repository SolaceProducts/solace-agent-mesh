-- +goose Up

ALTER TABLE task_checkpoints ADD COLUMN owner_id TEXT;
ALTER TABLE task_checkpoints ADD COLUMN lease_expires_at TEXT;

CREATE INDEX IF NOT EXISTS idx_task_checkpoints_lease
    ON task_checkpoints(lease_expires_at);

-- +goose Down

DROP INDEX IF EXISTS idx_task_checkpoints_lease;
ALTER TABLE task_checkpoints DROP COLUMN lease_expires_at;
ALTER TABLE task_checkpoints DROP COLUMN owner_id;
