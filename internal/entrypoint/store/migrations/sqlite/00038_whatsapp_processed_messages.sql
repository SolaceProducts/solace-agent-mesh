-- +goose Up

CREATE TABLE whatsapp_processed_messages (
    gateway_id TEXT NOT NULL,
    wamid TEXT NOT NULL,
    state TEXT NOT NULL,
    claimed_at INTEGER NOT NULL,
    processed_at INTEGER,
    attempts INTEGER NOT NULL DEFAULT 0,
    failure_kind TEXT NOT NULL DEFAULT '',
    attempts_total INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (gateway_id, wamid)
);

CREATE INDEX IF NOT EXISTS idx_whatsapp_processed_claimed_at ON whatsapp_processed_messages(claimed_at);

-- +goose Down
DROP INDEX IF EXISTS idx_whatsapp_processed_claimed_at;
DROP TABLE IF EXISTS whatsapp_processed_messages;
