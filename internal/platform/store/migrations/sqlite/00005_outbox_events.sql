-- +goose Up
CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    published INTEGER NOT NULL DEFAULT 0,
    published_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(published, created_at);
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON outbox_events(aggregate_id);

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
