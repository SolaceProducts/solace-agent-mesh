-- +goose Up
ALTER TABLE config_proposals ADD COLUMN finding_ref TEXT;

-- +goose Down
ALTER TABLE config_proposals DROP COLUMN finding_ref;
