-- +goose Up
-- +goose NO TRANSACTION

ALTER TABLE agents ADD COLUMN IF NOT EXISTS skill_refs TEXT;

-- +goose Down

ALTER TABLE agents DROP COLUMN IF EXISTS skill_refs;
