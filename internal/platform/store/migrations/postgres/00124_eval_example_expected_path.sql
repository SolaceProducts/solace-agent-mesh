-- +goose Up
ALTER TABLE eval_examples ADD COLUMN IF NOT EXISTS expected_path TEXT;

-- +goose Down
ALTER TABLE eval_examples DROP COLUMN IF EXISTS expected_path;
