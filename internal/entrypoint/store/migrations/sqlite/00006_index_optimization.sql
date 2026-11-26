-- +goose Up

DROP INDEX IF EXISTS idx_tasks_initial_request_text;
DROP INDEX IF EXISTS idx_tasks_events_buffered;
DROP INDEX IF EXISTS idx_shared_links_require_auth;

DROP INDEX IF EXISTS idx_sse_event_buffer_consumed;

DROP INDEX IF EXISTS idx_sse_event_buffer_task_id;
DROP INDEX IF EXISTS idx_shared_artifacts_share_id;

DROP INDEX IF EXISTS idx_sessions_user_deleted;
CREATE INDEX idx_sessions_list ON sessions(user_id, deleted_at, created_time DESC);

DROP INDEX IF EXISTS idx_tasks_user_id;
CREATE INDEX idx_tasks_user_starttime ON tasks(user_id, start_time DESC);

CREATE INDEX idx_tasks_session_bg ON tasks(session_id, execution_mode, end_time);
CREATE INDEX idx_tasks_parent_active ON tasks(parent_task_id, end_time);
CREATE INDEX idx_sse_session_unconsumed ON sse_event_buffer(session_id, consumed, event_sequence);
CREATE INDEX idx_feedback_session ON feedback(session_id);
CREATE INDEX idx_shared_links_list ON shared_links(user_id, deleted_at, created_time DESC);

-- +goose Down

DROP INDEX IF EXISTS idx_shared_links_list;
DROP INDEX IF EXISTS idx_feedback_session;
DROP INDEX IF EXISTS idx_sse_session_unconsumed;
DROP INDEX IF EXISTS idx_tasks_parent_active;
DROP INDEX IF EXISTS idx_tasks_session_bg;

DROP INDEX IF EXISTS idx_tasks_user_starttime;
CREATE INDEX idx_tasks_user_id ON tasks(user_id);

DROP INDEX IF EXISTS idx_sessions_list;
CREATE INDEX idx_sessions_user_deleted ON sessions(user_id, deleted_at);

CREATE INDEX idx_shared_artifacts_share_id ON shared_artifacts(share_id);
CREATE INDEX idx_sse_event_buffer_task_id ON sse_event_buffer(task_id);
CREATE INDEX idx_sse_event_buffer_consumed ON sse_event_buffer(consumed);
CREATE INDEX idx_shared_links_require_auth ON shared_links(require_authentication);
CREATE INDEX idx_tasks_events_buffered ON tasks(events_buffered);
CREATE INDEX idx_tasks_initial_request_text ON tasks(initial_request_text);
