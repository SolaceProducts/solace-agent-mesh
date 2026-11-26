-- +goose Up

ALTER TABLE scheduled_tasks ADD COLUMN notification_config TEXT;
ALTER TABLE scheduled_task_executions ADD COLUMN notifications_sent TEXT;

-- +goose Down

