-- +goose Up
ALTER TABLE model_configurations ADD COLUMN IF NOT EXISTS model_type TEXT NOT NULL DEFAULT 'chat';

-- +goose Down
ALTER TABLE model_configurations DROP COLUMN IF EXISTS model_type;
