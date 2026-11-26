-- +goose Up

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS retry_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS next_attempt_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS dead INTEGER NOT NULL DEFAULT 0;

DROP INDEX IF EXISTS idx_outbox_unpublished;
CREATE INDEX IF NOT EXISTS idx_outbox_dispatch ON outbox_events(published, dead, next_attempt_at);

-- +goose Down

DROP INDEX IF EXISTS idx_outbox_dispatch;
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(published, created_at);

ALTER TABLE outbox_events DROP COLUMN IF EXISTS dead;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS last_error;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS retry_count;
