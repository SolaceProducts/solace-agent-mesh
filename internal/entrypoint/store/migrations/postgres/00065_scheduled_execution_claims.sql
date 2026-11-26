-- +goose Up

ALTER TABLE scheduled_task_executions
    ADD COLUMN IF NOT EXISTS submitted_at BIGINT DEFAULT 0,
    ADD COLUMN IF NOT EXISTS finalized_at BIGINT DEFAULT 0;

-- +goose Down

ALTER TABLE scheduled_task_executions
    DROP COLUMN IF EXISTS finalized_at,
    DROP COLUMN IF EXISTS submitted_at;
