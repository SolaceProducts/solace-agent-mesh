-- +goose Up

ALTER TABLE tool_packages ADD COLUMN content_hash TEXT;

-- +goose Down
ALTER TABLE tool_packages DROP COLUMN content_hash;
