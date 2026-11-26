-- +goose Up
-- +goose NO TRANSACTION

ALTER TABLE connectors ADD COLUMN IF NOT EXISTS resource_key TEXT;

-- +goose Down

ALTER TABLE connectors DROP COLUMN IF EXISTS resource_key;
