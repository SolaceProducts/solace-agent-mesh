-- +goose Up
ALTER TABLE model_configurations ADD COLUMN IF NOT EXISTS max_input_tokens INTEGER;

-- +goose Down
ALTER TABLE model_configurations DROP COLUMN IF EXISTS max_input_tokens;
