-- +goose Up
-- +goose NO TRANSACTION
ALTER TABLE connectors ADD COLUMN resource_key TEXT;

-- +goose Down
