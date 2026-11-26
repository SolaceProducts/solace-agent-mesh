-- +goose Up

ALTER TABLE workflow_paused ADD COLUMN IF NOT EXISTS task_id TEXT;

CREATE INDEX IF NOT EXISTS idx_workflow_paused_task
    ON workflow_paused (task_id);

-- +goose Down

DROP INDEX IF EXISTS idx_workflow_paused_task;
ALTER TABLE workflow_paused DROP COLUMN IF EXISTS task_id;
