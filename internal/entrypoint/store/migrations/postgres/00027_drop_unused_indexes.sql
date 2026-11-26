-- +goose Up

DROP INDEX IF EXISTS idx_task_events_user_id;
DROP INDEX IF EXISTS idx_sessions_deleted_at;
DROP INDEX IF EXISTS idx_tasks_kind;
DROP INDEX IF EXISTS idx_chat_tasks_created_time;
DROP INDEX IF EXISTS idx_sessions_last_viewed;

-- +goose Down

CREATE INDEX IF NOT EXISTS idx_task_events_user_id ON task_events(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_deleted_at ON sessions(deleted_at);
CREATE INDEX IF NOT EXISTS idx_tasks_kind ON tasks(kind);
CREATE INDEX IF NOT EXISTS idx_chat_tasks_created_time ON chat_tasks(created_time);
CREATE INDEX IF NOT EXISTS idx_sessions_last_viewed ON sessions(last_viewed_at);
