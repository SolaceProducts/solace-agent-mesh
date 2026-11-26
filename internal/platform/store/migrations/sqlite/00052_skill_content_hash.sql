-- +goose Up

ALTER TABLE skills ADD COLUMN content_hash TEXT;

-- +goose Down
ALTER TABLE skills DROP COLUMN content_hash;
