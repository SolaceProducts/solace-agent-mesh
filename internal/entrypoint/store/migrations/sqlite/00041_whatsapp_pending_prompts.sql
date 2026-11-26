-- +goose Up

CREATE TABLE whatsapp_pending_prompts (
    gateway_id TEXT NOT NULL,
    sender_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    agent_name TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    surface_json TEXT NOT NULL,
    outbound_id TEXT NOT NULL DEFAULT '',
    prompt_wamid TEXT NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (gateway_id, sender_id)
);
CREATE INDEX IF NOT EXISTS idx_whatsapp_pending_prompts_expires
    ON whatsapp_pending_prompts(gateway_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_whatsapp_pending_prompts_expires_all
    ON whatsapp_pending_prompts(expires_at);

-- +goose Down

DROP TABLE whatsapp_pending_prompts;
