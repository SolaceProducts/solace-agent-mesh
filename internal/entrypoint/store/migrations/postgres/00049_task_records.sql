-- +goose Up

CREATE TABLE IF NOT EXISTS task_records (
    task_id              UUID PRIMARY KEY,
    gateway_id           TEXT NOT NULL,
    agent_name           TEXT NOT NULL,
    user_id              TEXT NOT NULL,
    session_id           TEXT NOT NULL DEFAULT '',
    trace_id             TEXT NOT NULL DEFAULT '',
    trigger_kind         TEXT NOT NULL DEFAULT '',
    system_initiated     INTEGER NOT NULL DEFAULT 0,
    machine_ingress      INTEGER NOT NULL DEFAULT 0,
    surface              TEXT NOT NULL DEFAULT '',
    agent_type           TEXT NOT NULL DEFAULT '',
    analytics_session_id TEXT NOT NULL DEFAULT '',
    model_family         TEXT NOT NULL DEFAULT '',
    model_provider       TEXT NOT NULL DEFAULT '',
    model_served_from    TEXT NOT NULL DEFAULT '',
    run_reported         INTEGER NOT NULL DEFAULT 0,
    last_seen_at         BIGINT NOT NULL,
    cancel_requested_at  BIGINT,
    cancel_claimed_at    BIGINT,
    created_at           BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_records_session_id ON task_records(session_id);
CREATE INDEX IF NOT EXISTS idx_task_records_last_seen_at ON task_records(last_seen_at);
CREATE INDEX IF NOT EXISTS idx_task_records_cancel_owed ON task_records(gateway_id, cancel_claimed_at)
    WHERE cancel_requested_at IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS task_records;
