-- +goose Up

ALTER TABLE workflow_checkpoint ADD COLUMN wake_at INTEGER;

-- +goose Down

ALTER TABLE workflow_checkpoint DROP COLUMN wake_at;
