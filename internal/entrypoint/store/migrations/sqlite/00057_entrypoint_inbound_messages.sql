-- +goose Up

CREATE TABLE IF NOT EXISTS entrypoint_inbound_messages (
    gateway_id  TEXT NOT NULL,
    message_id  TEXT NOT NULL,
    state       TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '',
    claimed_at  INTEGER NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    received_at INTEGER NOT NULL,
    PRIMARY KEY (gateway_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_entrypoint_inbound_messages_lapsed
    ON entrypoint_inbound_messages (gateway_id, state, claimed_at);

CREATE INDEX IF NOT EXISTS idx_entrypoint_inbound_messages_received_at
    ON entrypoint_inbound_messages (received_at);

-- +goose Down

DROP INDEX IF EXISTS idx_entrypoint_inbound_messages_received_at;
DROP INDEX IF EXISTS idx_entrypoint_inbound_messages_lapsed;
DROP TABLE IF EXISTS entrypoint_inbound_messages;
