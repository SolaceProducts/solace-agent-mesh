-- +goose Up
-- +goose NO TRANSACTION
ALTER TABLE agents ADD COLUMN skill_refs TEXT;

-- +goose Down
