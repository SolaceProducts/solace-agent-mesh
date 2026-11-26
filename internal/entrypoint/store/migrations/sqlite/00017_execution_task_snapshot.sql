-- +goose Up

ALTER TABLE scheduled_task_executions ADD COLUMN task_snapshot TEXT;

-- +goose Down

