-- +goose Up

ALTER TABLE tasks ADD COLUMN channel TEXT;
ALTER TABLE tasks ADD COLUMN entrypoint_id TEXT;
ALTER TABLE tasks ADD COLUMN entrypoint_name TEXT;
ALTER TABLE tasks ADD COLUMN trigger_source TEXT;

-- +goose Down

ALTER TABLE tasks DROP COLUMN trigger_source;
ALTER TABLE tasks DROP COLUMN entrypoint_name;
ALTER TABLE tasks DROP COLUMN entrypoint_id;
ALTER TABLE tasks DROP COLUMN channel;
