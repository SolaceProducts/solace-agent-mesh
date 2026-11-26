-- +goose Up
ALTER TABLE model_configurations ADD COLUMN model_type TEXT NOT NULL DEFAULT 'chat';

-- +goose Down
ALTER TABLE model_configurations DROP COLUMN model_type;
