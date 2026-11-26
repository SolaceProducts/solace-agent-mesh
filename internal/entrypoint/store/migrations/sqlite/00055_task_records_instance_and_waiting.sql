-- +goose Up

ALTER TABLE task_records ADD COLUMN gateway_instance_id TEXT NOT NULL DEFAULT '';
ALTER TABLE task_records ADD COLUMN waiting_request_id  TEXT;

-- +goose Down

ALTER TABLE task_records DROP COLUMN waiting_request_id;
ALTER TABLE task_records DROP COLUMN gateway_instance_id;
