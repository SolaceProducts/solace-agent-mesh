
-- +goose Up
ALTER TABLE tool_packages ADD COLUMN capabilities JSON NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE tool_packages DROP COLUMN capabilities;
