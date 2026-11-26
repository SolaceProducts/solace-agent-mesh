-- +goose Up

CREATE TABLE IF NOT EXISTS workflow_checkpoint (
    execution_id    TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL,
    workflow_name   TEXT NOT NULL,
    definition_hash TEXT NOT NULL DEFAULT '',
    state_snapshot  TEXT NOT NULL DEFAULT '',
    exec_context    TEXT NOT NULL DEFAULT '',
    deadline_at     INTEGER,
    status          TEXT NOT NULL DEFAULT 'held',
    held_by         TEXT NOT NULL DEFAULT '',
    holder_epoch    INTEGER NOT NULL DEFAULT 0,
    open_pauses     TEXT NOT NULL DEFAULT '[]',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    answered_at     INTEGER,
    ended_at        INTEGER
);

CREATE INDEX IF NOT EXISTS idx_workflow_checkpoint_task
    ON workflow_checkpoint (task_id);

CREATE INDEX IF NOT EXISTS idx_workflow_checkpoint_status_ended
    ON workflow_checkpoint (status, ended_at);

CREATE INDEX IF NOT EXISTS idx_workflow_checkpoint_deadline
    ON workflow_checkpoint (deadline_at);

ALTER TABLE workflow_paused ADD COLUMN answer TEXT;

-- +goose Down

ALTER TABLE workflow_paused DROP COLUMN answer;
DROP TABLE IF EXISTS workflow_checkpoint;
