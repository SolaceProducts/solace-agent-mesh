-- +goose Up

ALTER TABLE projects ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE projects DROP COLUMN IF EXISTS is_pinned;
