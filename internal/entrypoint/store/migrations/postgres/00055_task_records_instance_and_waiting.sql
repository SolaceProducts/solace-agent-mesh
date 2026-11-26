-- +goose Up

ALTER TABLE task_records ADD COLUMN IF NOT EXISTS gateway_instance_id TEXT NOT NULL DEFAULT '';
ALTER TABLE task_records ADD COLUMN IF NOT EXISTS waiting_request_id  TEXT;

-- +goose Down

ALTER TABLE task_records DROP COLUMN IF EXISTS waiting_request_id;
ALTER TABLE task_records DROP COLUMN IF EXISTS gateway_instance_id;
