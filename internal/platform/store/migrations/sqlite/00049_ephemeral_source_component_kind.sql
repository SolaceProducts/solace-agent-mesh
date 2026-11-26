-- +goose Up

ALTER TABLE agents ADD COLUMN source_component_kind TEXT;

-- +goose Down

ALTER TABLE agents DROP COLUMN source_component_kind;
