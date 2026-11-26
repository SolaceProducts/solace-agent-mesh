-- +goose Up

CREATE TABLE IF NOT EXISTS workflow_paused (
    request_id        TEXT PRIMARY KEY,
    execution_id      TEXT NOT NULL,
    workflow_name     TEXT NOT NULL,
    node_id           TEXT NOT NULL,
    requester_addr    TEXT NOT NULL,
    recipient_user_id TEXT NOT NULL DEFAULT '',
    state_snapshot    TEXT NOT NULL,
    exec_context      TEXT NOT NULL,
    routing           TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'paused',
    expires_at        BIGINT,
    created_at        BIGINT NOT NULL,
    resolved_at       BIGINT
);

CREATE INDEX IF NOT EXISTS idx_workflow_paused_status_expires
    ON workflow_paused (status, expires_at);

CREATE INDEX IF NOT EXISTS idx_workflow_paused_execution
    ON workflow_paused (execution_id);

-- +goose Down
DROP TABLE workflow_paused;
