-- +goose Up

ALTER TABLE scheduled_tasks ADD COLUMN run_as TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN run_as TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN refusal_cause TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN refusal_notice TEXT;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN refusal_notice;
ALTER TABLE scheduled_task_executions DROP COLUMN refusal_cause;
ALTER TABLE scheduled_task_executions DROP COLUMN run_as;
ALTER TABLE scheduled_tasks DROP COLUMN run_as;
