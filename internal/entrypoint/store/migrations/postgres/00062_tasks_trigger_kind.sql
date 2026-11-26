-- +goose Up

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS trigger_kind TEXT;

-- +goose Down

ALTER TABLE tasks DROP COLUMN IF EXISTS trigger_kind;
