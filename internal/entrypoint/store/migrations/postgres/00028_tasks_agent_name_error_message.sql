-- +goose Up

ALTER TABLE tasks ADD COLUMN agent_name TEXT;
ALTER TABLE tasks ADD COLUMN error_message TEXT;

-- +goose Down

ALTER TABLE tasks DROP COLUMN IF EXISTS error_message;
ALTER TABLE tasks DROP COLUMN IF EXISTS agent_name;
