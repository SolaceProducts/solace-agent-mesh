-- +goose Up

CREATE TABLE IF NOT EXISTS workflow_checkpoint (
    execution_id    uuid PRIMARY KEY,
    task_id         TEXT NOT NULL,
    workflow_name   TEXT NOT NULL,
    definition_hash TEXT NOT NULL DEFAULT '',
    state_snapshot  TEXT NOT NULL DEFAULT '',
    exec_context    TEXT NOT NULL DEFAULT '',
    deadline_at     BIGINT,
    status          TEXT NOT NULL DEFAULT 'held',
    held_by         TEXT NOT NULL DEFAULT '',
    holder_epoch    BIGINT NOT NULL DEFAULT 0,
    open_pauses     TEXT NOT NULL DEFAULT '[]',
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL,
    answered_at     BIGINT,
    ended_at        BIGINT
);

CREATE INDEX IF NOT EXISTS idx_workflow_checkpoint_task
    ON workflow_checkpoint (task_id);

CREATE INDEX IF NOT EXISTS idx_workflow_checkpoint_status_ended
    ON workflow_checkpoint (status, ended_at);

CREATE INDEX IF NOT EXISTS idx_workflow_checkpoint_deadline
    ON workflow_checkpoint (deadline_at);

ALTER TABLE workflow_paused ADD COLUMN IF NOT EXISTS answer TEXT;

-- +goose Down

ALTER TABLE workflow_paused DROP COLUMN IF EXISTS answer;
DROP TABLE IF EXISTS workflow_checkpoint;
