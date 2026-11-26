-- +goose Up

ALTER TABLE task_pending_calls ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE task_pending_calls DROP COLUMN tool_call_id;
