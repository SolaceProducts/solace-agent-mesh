-- +goose Up
ALTER TABLE eval_examples ADD COLUMN expected_facts TEXT;

-- +goose Down
ALTER TABLE eval_examples DROP COLUMN expected_facts;
