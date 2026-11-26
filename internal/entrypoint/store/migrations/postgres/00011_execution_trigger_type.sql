-- +goose Up

ALTER TABLE scheduled_task_executions ADD COLUMN trigger_type TEXT NOT NULL DEFAULT 'scheduled'
    CHECK (trigger_type IN ('scheduled', 'manual'));
ALTER TABLE scheduled_task_executions ADD COLUMN triggered_by TEXT;
CREATE INDEX idx_execution_trigger_type ON scheduled_task_executions(trigger_type);

-- +goose Down

DROP INDEX IF EXISTS idx_execution_trigger_type;
ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS triggered_by;
ALTER TABLE scheduled_task_executions DROP COLUMN IF EXISTS trigger_type;
