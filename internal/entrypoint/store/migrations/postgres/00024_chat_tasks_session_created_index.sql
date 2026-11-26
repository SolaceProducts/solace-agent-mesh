-- +goose Up

CREATE INDEX IF NOT EXISTS idx_chat_tasks_session_created
    ON chat_tasks(session_id, created_time);

CREATE INDEX IF NOT EXISTS idx_tasks_session_user
    ON tasks(session_id, user_id);

-- +goose Down

DROP INDEX IF EXISTS idx_tasks_session_user;
DROP INDEX IF EXISTS idx_chat_tasks_session_created;
