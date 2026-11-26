-- +goose Up
ALTER TABLE eval_examples ADD COLUMN IF NOT EXISTS expected_facts TEXT;

-- +goose Down
ALTER TABLE eval_examples DROP COLUMN IF EXISTS expected_facts;
