-- +goose Up

ALTER TABLE task_events ADD COLUMN trace_id TEXT;

-- +goose Down

ALTER TABLE task_events DROP COLUMN trace_id;
