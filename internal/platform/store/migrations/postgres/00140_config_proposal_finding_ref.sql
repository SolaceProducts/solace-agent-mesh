-- +goose Up
ALTER TABLE config_proposals ADD COLUMN IF NOT EXISTS finding_ref UUID;

-- +goose Down
ALTER TABLE config_proposals DROP COLUMN IF EXISTS finding_ref;
