
-- +goose Up
ALTER TABLE workflows DROP COLUMN IF EXISTS display_name;

-- +goose Down
ALTER TABLE workflows ADD COLUMN IF NOT EXISTS display_name TEXT;
