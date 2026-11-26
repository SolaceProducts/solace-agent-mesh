-- +goose Up

ALTER TABLE scheduled_task_executions ADD COLUMN submitted_at BIGINT DEFAULT 0;
ALTER TABLE scheduled_task_executions ADD COLUMN finalized_at BIGINT DEFAULT 0;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN finalized_at;
ALTER TABLE scheduled_task_executions DROP COLUMN submitted_at;
