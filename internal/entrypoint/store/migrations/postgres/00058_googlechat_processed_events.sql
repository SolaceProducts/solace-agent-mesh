-- +goose Up

CREATE TABLE IF NOT EXISTS googlechat_processed_events (
    gateway_id TEXT NOT NULL,
    event_key TEXT NOT NULL,
    state TEXT NOT NULL,
    claimed_at BIGINT NOT NULL,
    processed_at BIGINT,
    attempts INTEGER NOT NULL DEFAULT 0,
    failure_kind TEXT NOT NULL DEFAULT '',
    attempts_total INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (gateway_id, event_key)
);

CREATE INDEX IF NOT EXISTS idx_googlechat_processed_claimed_at ON googlechat_processed_events(claimed_at);

-- +goose Down
DROP INDEX IF EXISTS idx_googlechat_processed_claimed_at;
DROP TABLE IF EXISTS googlechat_processed_events;
