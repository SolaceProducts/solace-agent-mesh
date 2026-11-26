-- +goose Up

ALTER TABLE task_events ADD COLUMN is_delta INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_task_events_delta_created ON task_events(created_time) WHERE is_delta = 1;

-- +goose Down

DROP INDEX IF EXISTS idx_task_events_delta_created;
ALTER TABLE task_events DROP COLUMN is_delta;
