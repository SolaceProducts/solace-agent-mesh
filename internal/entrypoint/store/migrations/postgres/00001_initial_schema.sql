-- +goose Up

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    name TEXT,
    user_id TEXT NOT NULL,
    agent_id TEXT,
    project_id TEXT,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,
    deleted_at BIGINT,
    deleted_by TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_project_id ON sessions(project_id);
CREATE INDEX IF NOT EXISTS idx_sessions_deleted_at ON sessions(deleted_at);
CREATE INDEX IF NOT EXISTS idx_sessions_user_deleted ON sessions(user_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_sessions_updated_time ON sessions(updated_time);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id_updated_time ON sessions(user_id, updated_time);

CREATE TABLE IF NOT EXISTS chat_tasks (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    user_message TEXT,
    message_bubbles TEXT NOT NULL,
    task_metadata TEXT,
    created_time BIGINT NOT NULL,
    updated_time BIGINT
);
CREATE INDEX IF NOT EXISTS idx_chat_tasks_session_id ON chat_tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_chat_tasks_user_id ON chat_tasks(user_id);
CREATE INDEX IF NOT EXISTS idx_chat_tasks_created_time ON chat_tasks(created_time);

CREATE TABLE IF NOT EXISTS tasks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    parent_task_id TEXT,
    start_time BIGINT NOT NULL,
    end_time BIGINT,
    status TEXT,
    initial_request_text TEXT,
    total_input_tokens INTEGER,
    total_output_tokens INTEGER,
    total_cached_input_tokens INTEGER,
    token_usage_details TEXT,
    execution_mode TEXT DEFAULT 'foreground',
    last_activity_time BIGINT,
    background_execution_enabled INTEGER DEFAULT 0,
    max_execution_time_ms BIGINT,
    session_id TEXT,
    events_buffered INTEGER DEFAULT 0,
    events_consumed INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_tasks_user_id ON tasks(user_id);
CREATE INDEX IF NOT EXISTS idx_tasks_parent_task_id ON tasks(parent_task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_start_time ON tasks(start_time);
CREATE INDEX IF NOT EXISTS idx_tasks_execution_mode ON tasks(execution_mode);
CREATE INDEX IF NOT EXISTS idx_tasks_last_activity ON tasks(last_activity_time);
CREATE INDEX IF NOT EXISTS idx_tasks_session_id ON tasks(session_id);
CREATE INDEX IF NOT EXISTS idx_tasks_initial_request_text ON tasks(initial_request_text);
CREATE INDEX IF NOT EXISTS idx_tasks_events_buffered ON tasks(events_buffered);

CREATE TABLE IF NOT EXISTS task_events (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    user_id TEXT,
    created_time BIGINT NOT NULL,
    topic TEXT NOT NULL,
    direction TEXT NOT NULL,
    payload TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_events_task_id ON task_events(task_id);
CREATE INDEX IF NOT EXISTS idx_task_events_user_id ON task_events(user_id);

CREATE TABLE IF NOT EXISTS sse_event_buffer (
    id SERIAL PRIMARY KEY,
    task_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    event_sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    event_data TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    consumed INTEGER NOT NULL DEFAULT 0,
    consumed_at BIGINT,
    UNIQUE(task_id, event_sequence)
);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_id ON sse_event_buffer(task_id);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_session_id ON sse_event_buffer(session_id);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_created_at ON sse_event_buffer(created_at);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_consumed ON sse_event_buffer(consumed);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_consumed ON sse_event_buffer(task_id, consumed);

CREATE TABLE IF NOT EXISTS feedback (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    rating TEXT NOT NULL,
    comment TEXT,
    created_time BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedback_task_id ON feedback(task_id);
CREATE INDEX IF NOT EXISTS idx_feedback_user_id ON feedback(user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_created_time ON feedback(created_time);

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    user_id TEXT NOT NULL,
    description TEXT,
    system_prompt TEXT,
    default_agent_id TEXT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT,
    deleted_at BIGINT,
    deleted_by TEXT
);
CREATE INDEX IF NOT EXISTS idx_projects_deleted_at ON projects(deleted_at);
CREATE INDEX IF NOT EXISTS idx_projects_user_deleted ON projects(user_id, deleted_at);

CREATE TABLE IF NOT EXISTS project_users (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    added_at BIGINT NOT NULL,
    added_by_user_id TEXT NOT NULL,
    UNIQUE(project_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_project_users_project_id ON project_users(project_id);
CREATE INDEX IF NOT EXISTS idx_project_users_user_id ON project_users(user_id);
CREATE INDEX IF NOT EXISTS idx_project_users_user_project ON project_users(user_id, project_id);

CREATE TABLE IF NOT EXISTS prompt_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    category TEXT,
    command TEXT,
    user_id TEXT NOT NULL,
    author_name TEXT,
    production_prompt_id TEXT,
    is_shared INTEGER NOT NULL DEFAULT 0,
    is_pinned INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_prompt_groups_name ON prompt_groups(name);
CREATE INDEX IF NOT EXISTS idx_prompt_groups_category ON prompt_groups(category);
CREATE INDEX IF NOT EXISTS idx_prompt_groups_command ON prompt_groups(command);
CREATE INDEX IF NOT EXISTS idx_prompt_groups_user_id ON prompt_groups(user_id);
CREATE INDEX IF NOT EXISTS idx_prompt_groups_is_pinned ON prompt_groups(is_pinned);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_groups_command_user_id ON prompt_groups(command, user_id);

CREATE TABLE IF NOT EXISTS prompts (
    id TEXT PRIMARY KEY,
    prompt_text TEXT NOT NULL,
    group_id TEXT NOT NULL REFERENCES prompt_groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    name TEXT,
    description TEXT,
    category TEXT,
    command TEXT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_prompts_group_id ON prompts(group_id);
CREATE INDEX IF NOT EXISTS idx_prompts_user_id ON prompts(user_id);

CREATE TABLE IF NOT EXISTS prompt_group_users (
    id TEXT PRIMARY KEY,
    prompt_group_id TEXT NOT NULL REFERENCES prompt_groups(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'viewer',
    added_at BIGINT NOT NULL,
    added_by_user_id TEXT NOT NULL,
    UNIQUE(prompt_group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_prompt_group_id ON prompt_group_users(prompt_group_id);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_user_id ON prompt_group_users(user_id);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_user_prompt ON prompt_group_users(user_id, prompt_group_id);
CREATE INDEX IF NOT EXISTS idx_prompt_group_users_user_role ON prompt_group_users(user_id, role);

-- +goose Down
DROP TABLE IF EXISTS prompt_group_users;
DROP TABLE IF EXISTS prompts;
DROP TABLE IF EXISTS prompt_groups;
DROP TABLE IF EXISTS project_users;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS feedback;
DROP TABLE IF EXISTS sse_event_buffer;
DROP TABLE IF EXISTS task_events;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS chat_tasks;
DROP TABLE IF EXISTS sessions;
