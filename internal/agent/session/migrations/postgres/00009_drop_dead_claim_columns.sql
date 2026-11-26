-- +goose Up

ALTER TABLE task_checkpoints DROP COLUMN claimed_at;
ALTER TABLE task_checkpoints DROP COLUMN claimed;

-- +goose Down

ALTER TABLE task_checkpoints ADD COLUMN claimed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE task_checkpoints ADD COLUMN claimed_at TEXT;
