-- +goose Up
ALTER TABLE model_configurations ADD COLUMN max_input_tokens INTEGER;

-- +goose Down
ALTER TABLE model_configurations DROP COLUMN max_input_tokens;
