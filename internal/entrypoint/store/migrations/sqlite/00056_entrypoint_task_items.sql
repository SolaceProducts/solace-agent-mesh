-- +goose Up

CREATE TABLE IF NOT EXISTS entrypoint_task_items (
    task_id    TEXT NOT NULL REFERENCES task_records(task_id) ON DELETE CASCADE,
    item       TEXT NOT NULL,
    value      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (task_id, item)
);

-- +goose Down

DROP TABLE IF EXISTS entrypoint_task_items;
