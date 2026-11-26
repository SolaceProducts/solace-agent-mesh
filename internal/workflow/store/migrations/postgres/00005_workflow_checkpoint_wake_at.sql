-- +goose Up

ALTER TABLE workflow_checkpoint ADD COLUMN IF NOT EXISTS wake_at BIGINT;

-- +goose Down

ALTER TABLE workflow_checkpoint DROP COLUMN IF EXISTS wake_at;
