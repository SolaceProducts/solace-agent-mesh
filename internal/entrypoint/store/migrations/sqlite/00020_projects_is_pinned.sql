-- +goose Up

ALTER TABLE projects ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0;

-- +goose Down

