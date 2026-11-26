-- +goose Up

ALTER TABLE sse_event_buffer ADD COLUMN completed INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE sse_event_buffer DROP COLUMN IF EXISTS completed;
