-- +goose Up

ALTER TABLE tasks ADD COLUMN trigger_kind TEXT;

-- +goose Down

ALTER TABLE tasks DROP COLUMN trigger_kind;
