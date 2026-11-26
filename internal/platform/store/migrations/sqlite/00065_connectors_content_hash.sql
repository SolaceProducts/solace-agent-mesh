-- +goose Up

ALTER TABLE connectors ADD COLUMN content_hash TEXT;

-- +goose Down
ALTER TABLE connectors DROP COLUMN content_hash;
