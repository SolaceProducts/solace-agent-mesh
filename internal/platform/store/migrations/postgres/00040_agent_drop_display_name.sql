
-- +goose Up
ALTER TABLE agents DROP COLUMN IF EXISTS display_name;

-- +goose Down
ALTER TABLE agents ADD COLUMN IF NOT EXISTS display_name TEXT;
