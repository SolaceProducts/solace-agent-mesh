-- +goose Up

ALTER TABLE sse_event_buffer ADD COLUMN broker_message_id TEXT;

-- +goose Down

ALTER TABLE sse_event_buffer DROP COLUMN broker_message_id;
