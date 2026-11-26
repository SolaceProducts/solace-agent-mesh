-- +goose Up

CREATE INDEX IF NOT EXISTS idx_tasks_user_kind_lastactivity
    ON tasks(user_id, kind, last_activity_time DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_tasks_user_kind_lastactivity;
