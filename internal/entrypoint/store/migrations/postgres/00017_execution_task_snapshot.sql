-- +goose Up

ALTER TABLE scheduled_task_executions ADD COLUMN task_snapshot JSONB;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS task_snapshot;
