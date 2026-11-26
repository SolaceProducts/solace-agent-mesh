-- +goose Up

ALTER TABLE task_pending_calls ADD COLUMN tool_name TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE task_pending_calls DROP COLUMN tool_name;
