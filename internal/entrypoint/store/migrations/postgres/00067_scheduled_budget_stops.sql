-- +goose Up

ALTER TABLE scheduled_tasks ADD COLUMN IF NOT EXISTS budget_blocked_at BIGINT;
ALTER TABLE scheduled_task_executions ADD COLUMN IF NOT EXISTS budget_stop TEXT;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS budget_stop;
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS budget_blocked_at;
