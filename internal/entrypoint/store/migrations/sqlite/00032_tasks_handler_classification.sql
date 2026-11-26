-- +goose Up

ALTER TABLE tasks ADD COLUMN handler_name TEXT;
ALTER TABLE tasks ADD COLUMN handler_type TEXT;

CREATE INDEX IF NOT EXISTS idx_tasks_handler_type_name ON tasks(handler_type, handler_name);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_handler_type_name;
ALTER TABLE tasks DROP COLUMN handler_type;
ALTER TABLE tasks DROP COLUMN handler_name;
