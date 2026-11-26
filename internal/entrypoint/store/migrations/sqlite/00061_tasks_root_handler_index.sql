-- +goose Up

CREATE INDEX IF NOT EXISTS idx_tasks_root_handler_starttime ON tasks (handler_type, handler_name, start_time DESC) WHERE parent_task_id IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_tasks_root_handler_starttime;
