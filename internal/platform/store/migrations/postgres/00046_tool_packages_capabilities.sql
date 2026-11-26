
-- +goose Up
ALTER TABLE tool_packages ADD COLUMN capabilities JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE tool_packages DROP COLUMN capabilities;
