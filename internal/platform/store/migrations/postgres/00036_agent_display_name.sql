
-- +goose Up
ALTER TABLE agents ADD COLUMN display_name TEXT;

-- +goose Down
ALTER TABLE agents DROP COLUMN display_name;
