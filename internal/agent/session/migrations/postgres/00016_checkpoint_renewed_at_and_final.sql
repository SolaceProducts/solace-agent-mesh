-- +goose Up

ALTER TABLE task_checkpoints ADD COLUMN IF NOT EXISTS renewed_at BIGINT;
ALTER TABLE task_checkpoints ADD COLUMN IF NOT EXISTS final_message TEXT;

-- +goose Down

ALTER TABLE task_checkpoints DROP COLUMN IF EXISTS final_message;
ALTER TABLE task_checkpoints DROP COLUMN IF EXISTS renewed_at;
