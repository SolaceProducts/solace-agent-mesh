
-- +goose Up
ALTER TABLE workflows DROP COLUMN display_name;

-- +goose Down
ALTER TABLE workflows ADD COLUMN display_name TEXT;
