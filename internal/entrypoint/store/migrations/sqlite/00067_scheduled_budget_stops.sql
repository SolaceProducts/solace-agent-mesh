-- +goose Up

ALTER TABLE scheduled_tasks ADD COLUMN budget_blocked_at BIGINT;
ALTER TABLE scheduled_task_executions ADD COLUMN budget_stop TEXT;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN budget_stop;
ALTER TABLE scheduled_tasks DROP COLUMN budget_blocked_at;
