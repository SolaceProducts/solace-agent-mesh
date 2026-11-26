-- +goose Up

ALTER TABLE notifications ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE notifications DROP COLUMN IF EXISTS category;
