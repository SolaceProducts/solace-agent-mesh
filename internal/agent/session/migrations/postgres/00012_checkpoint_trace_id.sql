-- +goose Up

ALTER TABLE task_checkpoints ADD COLUMN trace_id TEXT NOT NULL DEFAULT '';
ALTER TABLE task_pending_calls ADD COLUMN trace_id TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE task_pending_calls DROP COLUMN trace_id;
ALTER TABLE task_checkpoints DROP COLUMN trace_id;
