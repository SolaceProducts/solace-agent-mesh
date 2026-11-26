-- +goose Up

ALTER TABLE tool_packages ADD COLUMN IF NOT EXISTS content_hash TEXT;

-- +goose Down

ALTER TABLE tool_packages DROP COLUMN IF EXISTS content_hash;
