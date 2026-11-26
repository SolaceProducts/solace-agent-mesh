-- +goose Up

ALTER TABLE entrypoint_inbound_messages ADD COLUMN task_id TEXT;
ALTER TABLE entrypoint_inbound_messages ADD COLUMN outcome TEXT;

-- +goose Down

ALTER TABLE entrypoint_inbound_messages DROP COLUMN outcome;
ALTER TABLE entrypoint_inbound_messages DROP COLUMN task_id;
