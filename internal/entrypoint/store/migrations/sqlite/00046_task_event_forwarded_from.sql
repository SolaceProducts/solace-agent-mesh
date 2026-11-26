-- +goose Up

ALTER TABLE task_events ADD COLUMN forwarded_from TEXT;

-- +goose Down

ALTER TABLE task_events DROP COLUMN forwarded_from;
