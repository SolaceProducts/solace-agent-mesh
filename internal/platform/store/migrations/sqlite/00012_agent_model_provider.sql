-- +goose Up
ALTER TABLE agents ADD COLUMN model_provider TEXT;

-- +goose Down
ALTER TABLE agents DROP COLUMN model_provider;
