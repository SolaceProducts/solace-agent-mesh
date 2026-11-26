-- +goose Up

ALTER TABLE task_events ADD COLUMN IF NOT EXISTS trace_id TEXT;

-- +goose Down

ALTER TABLE task_events DROP COLUMN IF EXISTS trace_id;
