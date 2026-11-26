-- +goose Up

ALTER TABLE scheduled_tasks ADD COLUMN notification_config TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN notifications_sent TEXT;

-- +goose Down

ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS notifications_sent;
ALTER TABLE scheduled_tasks DROP COLUMN IF EXISTS notification_config;
