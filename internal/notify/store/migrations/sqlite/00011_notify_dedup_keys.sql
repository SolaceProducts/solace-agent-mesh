-- +goose Up

CREATE TABLE IF NOT EXISTS notify_dedup_keys (
    user_id    TEXT NOT NULL,
    dedup      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, dedup)
);

CREATE INDEX IF NOT EXISTS idx_notify_dedup_keys_created
    ON notify_dedup_keys (created_at);

-- +goose Down
DROP TABLE notify_dedup_keys;
