
-- +goose Up
ALTER TABLE agents DROP COLUMN display_name;

-- +goose Down
ALTER TABLE agents ADD COLUMN display_name TEXT;
