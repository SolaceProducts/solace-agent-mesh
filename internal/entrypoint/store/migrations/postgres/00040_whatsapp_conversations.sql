-- +goose Up

CREATE TABLE whatsapp_conversations (
    gateway_id TEXT NOT NULL,
    sender_id TEXT NOT NULL,
    wa_id TEXT NOT NULL DEFAULT '',
    from_id TEXT NOT NULL DEFAULT '',
    last_inbound_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (gateway_id, sender_id)
);
CREATE INDEX IF NOT EXISTS idx_whatsapp_conversations_last_inbound
    ON whatsapp_conversations(last_inbound_at);

-- +goose Down

DROP TABLE whatsapp_conversations;
