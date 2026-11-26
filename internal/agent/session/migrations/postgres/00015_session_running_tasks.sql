-- +goose Up

CREATE TABLE IF NOT EXISTS session_running_tasks (
    task_id TEXT PRIMARY KEY,
    app_name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    lease_expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_running_tasks_session
    ON session_running_tasks(app_name, user_id, session_id);

-- +goose Down

DROP INDEX IF EXISTS idx_session_running_tasks_session;
DROP TABLE IF EXISTS session_running_tasks;
