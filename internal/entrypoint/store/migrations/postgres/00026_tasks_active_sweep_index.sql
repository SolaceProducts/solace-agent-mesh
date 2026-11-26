-- +goose Up

CREATE INDEX IF NOT EXISTS idx_tasks_active_last_activity
    ON tasks(last_activity_time)
    WHERE end_time IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_tasks_active_last_activity;
