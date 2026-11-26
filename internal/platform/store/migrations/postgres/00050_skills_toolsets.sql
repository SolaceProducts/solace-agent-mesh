
-- +goose Up
ALTER TABLE skills ADD COLUMN toolsets TEXT;

-- +goose Down
ALTER TABLE skills DROP COLUMN IF EXISTS toolsets;
