-- +goose Up

ALTER TABLE task_events ADD COLUMN IF NOT EXISTS forwarded_from TEXT;

-- +goose Down

ALTER TABLE task_events DROP COLUMN IF EXISTS forwarded_from;
