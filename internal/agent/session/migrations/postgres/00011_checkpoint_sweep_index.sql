-- +goose Up

CREATE INDEX IF NOT EXISTS idx_task_checkpoints_sweep
    ON task_checkpoints(agent_name, pending_count, lease_expires_at);

-- +goose Down

DROP INDEX IF EXISTS idx_task_checkpoints_sweep;
