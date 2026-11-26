-- +goose Up

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    namespace TEXT NOT NULL,
    user_id TEXT,
    created_by TEXT NOT NULL,
    schedule_type TEXT NOT NULL,
    schedule_expression TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    target_agent_name TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT 'agent',
    task_message TEXT NOT NULL,
    task_metadata TEXT,
    enabled INTEGER NOT NULL DEFAULT 1,
    max_retries INTEGER NOT NULL DEFAULT 0,
    retry_delay_seconds INTEGER NOT NULL DEFAULT 60,
    timeout_seconds INTEGER NOT NULL DEFAULT 3600,
    source TEXT NOT NULL,
    consecutive_failure_count INTEGER NOT NULL DEFAULT 0,
    run_count INTEGER NOT NULL DEFAULT 0,
    next_run_at BIGINT,
    last_run_at BIGINT,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    deleted_at BIGINT,
    deleted_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_sched_tasks_name ON scheduled_tasks(name);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_namespace ON scheduled_tasks(namespace);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_user_id ON scheduled_tasks(user_id);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_schedule_type ON scheduled_tasks(schedule_type);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_target_agent ON scheduled_tasks(target_agent_name);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_enabled ON scheduled_tasks(enabled);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_next_run ON scheduled_tasks(next_run_at);
CREATE INDEX IF NOT EXISTS idx_sched_tasks_deleted_at ON scheduled_tasks(deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sched_tasks_ns_name_active ON scheduled_tasks(namespace, name) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS scheduled_task_executions (
    id TEXT PRIMARY KEY,
    scheduled_task_id TEXT NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    a2a_task_id TEXT,
    scheduled_for BIGINT NOT NULL,
    started_at BIGINT,
    completed_at BIGINT,
    result_summary TEXT,
    error_message TEXT,
    artifacts TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_sched_exec_task_id ON scheduled_task_executions(scheduled_task_id);
CREATE INDEX IF NOT EXISTS idx_sched_exec_status ON scheduled_task_executions(status);
CREATE INDEX IF NOT EXISTS idx_sched_exec_a2a_task ON scheduled_task_executions(a2a_task_id);
CREATE INDEX IF NOT EXISTS idx_sched_exec_scheduled_for ON scheduled_task_executions(scheduled_for);

ALTER TABLE sessions ADD COLUMN source TEXT;

-- +goose Down

DROP TABLE IF EXISTS scheduled_task_executions;
DROP TABLE IF EXISTS scheduled_tasks;
ALTER TABLE sessions DROP COLUMN IF EXISTS source;
