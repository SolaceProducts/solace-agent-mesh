-- +goose Up

ALTER TABLE tasks ADD COLUMN agent_name TEXT;
ALTER TABLE tasks ADD COLUMN error_message TEXT;

-- +goose Down

ALTER TABLE tasks DROP COLUMN error_message;
ALTER TABLE tasks DROP COLUMN agent_name;
