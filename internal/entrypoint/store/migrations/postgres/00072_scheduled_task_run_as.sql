-- +goose Up

ALTER TABLE scheduled_tasks ADD COLUMN IF NOT EXISTS run_as TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN IF NOT EXISTS run_as TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN IF NOT EXISTS refusal_cause TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN IF NOT EXISTS refusal_notice TEXT;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS refusal_notice;
ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS refusal_cause;
ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS run_as;
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS run_as;
