-- +goose Up

CREATE TABLE IF NOT EXISTS outbox_events (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    published INTEGER NOT NULL DEFAULT 0,
    published_at BIGINT
);

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='outbox_events' AND column_name='published') THEN
    CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox_events(published, created_at);
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='outbox_events' AND column_name='aggregate_id') THEN
    CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON outbox_events(aggregate_id);
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down

DROP TABLE IF EXISTS outbox_events;
