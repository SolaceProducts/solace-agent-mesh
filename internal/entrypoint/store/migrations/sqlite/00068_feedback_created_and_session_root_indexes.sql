-- +goose Up

CREATE INDEX IF NOT EXISTS idx_feedback_created_at ON feedback (created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_session_roots ON tasks (session_id, start_time DESC) WHERE parent_task_id IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_feedback_created_at;
DROP INDEX IF EXISTS idx_tasks_session_roots;
