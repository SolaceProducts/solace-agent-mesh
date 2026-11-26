-- +goose Up

ALTER TABLE sse_event_buffer ADD COLUMN IF NOT EXISTS broker_message_id TEXT;

-- +goose Down

ALTER TABLE sse_event_buffer DROP COLUMN IF EXISTS broker_message_id;
