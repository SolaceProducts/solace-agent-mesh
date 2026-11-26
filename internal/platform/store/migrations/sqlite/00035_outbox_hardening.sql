-- +goose Up

ALTER TABLE outbox_events ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE outbox_events ADD COLUMN last_error TEXT;
ALTER TABLE outbox_events ADD COLUMN next_attempt_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE outbox_events ADD COLUMN dead INTEGER NOT NULL DEFAULT 0;

DROP INDEX IF EXISTS idx_outbox_unpublished;
CREATE INDEX IF NOT EXISTS idx_outbox_dispatch ON outbox_events(published, dead, next_attempt_at);

-- +goose Down

DROP INDEX IF EXISTS idx_outbox_dispatch;
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(published, created_at);

