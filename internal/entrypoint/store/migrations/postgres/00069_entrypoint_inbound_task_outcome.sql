-- +goose Up

ALTER TABLE entrypoint_inbound_messages ADD COLUMN IF NOT EXISTS task_id TEXT;
ALTER TABLE entrypoint_inbound_messages ADD COLUMN IF NOT EXISTS outcome TEXT;

-- +goose Down

ALTER TABLE entrypoint_inbound_messages DROP COLUMN IF EXISTS outcome;
ALTER TABLE entrypoint_inbound_messages DROP COLUMN IF EXISTS task_id;
