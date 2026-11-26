-- +goose Up

CREATE UNIQUE INDEX IF NOT EXISTS idx_sched_tasks_ns_user_name_active
    ON scheduled_tasks (namespace, COALESCE(user_id, ''), name) WHERE deleted_at IS NULL;

DROP INDEX IF EXISTS idx_sched_tasks_ns_name_active;

-- +goose Down

DROP INDEX IF EXISTS idx_sched_tasks_ns_user_name_active;

CREATE UNIQUE INDEX IF NOT EXISTS idx_sched_tasks_ns_name_active
    ON scheduled_tasks (namespace, name) WHERE deleted_at IS NULL;
