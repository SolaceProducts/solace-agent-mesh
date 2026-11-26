
-- +goose Up
CREATE TABLE IF NOT EXISTS whatsapp_pending_auth (
    gateway_id      TEXT NOT NULL,
    sender_id       TEXT NOT NULL,
    kind            TEXT NOT NULL,
    lookup_key      TEXT NOT NULL DEFAULT '',
    lookup_value_fp TEXT NOT NULL DEFAULT '',
    outbound_id     TEXT NOT NULL DEFAULT '',
    prompt_wamid    TEXT NOT NULL DEFAULT '',
    expires_at      INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (gateway_id, sender_id)
);

CREATE INDEX IF NOT EXISTS ix_whatsapp_pending_auth_expiry
    ON whatsapp_pending_auth(expires_at);

-- +goose Down
DROP INDEX IF EXISTS ix_whatsapp_pending_auth_expiry;
DROP TABLE IF EXISTS whatsapp_pending_auth;
