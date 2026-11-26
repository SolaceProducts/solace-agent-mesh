-- +goose Up

ALTER TABLE agents ADD COLUMN IF NOT EXISTS source_component_kind TEXT;

-- +goose Down

ALTER TABLE agents DROP COLUMN IF EXISTS source_component_kind;
