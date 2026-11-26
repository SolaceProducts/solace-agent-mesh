-- +goose Up

CREATE TABLE whatsapp_outbound (
    id TEXT PRIMARY KEY,
    gateway_id TEXT NOT NULL,
    recipient TEXT NOT NULL,
    task_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    agent_name TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    payload TEXT NOT NULL,
    artifact_ref TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    wamid TEXT NOT NULL DEFAULT '',
    error_code INTEGER NOT NULL DEFAULT 0,
    error_detail TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    not_before INTEGER NOT NULL DEFAULT 0,
    parked_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_whatsapp_outbound_claim
    ON whatsapp_outbound(gateway_id, state, recipient, id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_outbound_wamid
    ON whatsapp_outbound(wamid) WHERE wamid <> '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_whatsapp_outbound_parked
    ON whatsapp_outbound(gateway_id, recipient) WHERE state = 'parked';
CREATE INDEX IF NOT EXISTS idx_whatsapp_outbound_state_updated
    ON whatsapp_outbound(state, updated_at);
CREATE INDEX IF NOT EXISTS idx_whatsapp_outbound_state_created
    ON whatsapp_outbound(state, created_at);

-- +goose Down

DROP TABLE whatsapp_outbound;
