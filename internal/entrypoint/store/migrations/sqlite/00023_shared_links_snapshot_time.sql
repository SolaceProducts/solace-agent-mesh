-- +goose Up
ALTER TABLE shared_links ADD COLUMN snapshot_time INTEGER NOT NULL DEFAULT 0;
UPDATE shared_links SET snapshot_time = created_time WHERE snapshot_time = 0;

-- +goose Down
