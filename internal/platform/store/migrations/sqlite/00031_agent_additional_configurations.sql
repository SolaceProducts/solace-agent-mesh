
-- +goose Up
ALTER TABLE agents ADD COLUMN additional_configurations JSON NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE agents DROP COLUMN additional_configurations;
