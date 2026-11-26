-- +goose Up

ALTER TABLE skills ADD COLUMN IF NOT EXISTS content_hash TEXT;

-- +goose Down

ALTER TABLE skills DROP COLUMN IF EXISTS content_hash;
