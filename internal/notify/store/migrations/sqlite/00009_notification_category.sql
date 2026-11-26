-- +goose Up

ALTER TABLE notifications ADD COLUMN category TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE notifications DROP COLUMN category;
