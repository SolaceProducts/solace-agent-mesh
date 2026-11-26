
-- +goose Up
ALTER TABLE agents ADD COLUMN additional_configurations JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE agents DROP COLUMN additional_configurations;
