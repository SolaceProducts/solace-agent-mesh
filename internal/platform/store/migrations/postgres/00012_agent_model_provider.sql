-- +goose Up
ALTER TABLE agents ADD COLUMN IF NOT EXISTS model_provider TEXT;

-- +goose Down
ALTER TABLE agents DROP COLUMN IF EXISTS model_provider;
