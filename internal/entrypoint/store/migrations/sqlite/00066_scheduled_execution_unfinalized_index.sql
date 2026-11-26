-- +goose Up

CREATE INDEX IF NOT EXISTS idx_sched_exec_unfinalized ON scheduled_task_executions (scheduled_for) WHERE finalized_at IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_sched_exec_unfinalized;
