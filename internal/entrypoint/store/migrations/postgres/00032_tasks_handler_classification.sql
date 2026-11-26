-- +goose NO TRANSACTION
-- +goose Up

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS handler_name TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS handler_type TEXT;

DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_handler_type_name;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tasks_handler_type_name ON tasks(handler_type, handler_name);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_tasks_handler_type_name;
ALTER TABLE tasks DROP COLUMN IF EXISTS handler_type;
ALTER TABLE tasks DROP COLUMN IF EXISTS handler_name;
