-- +goose Up
ALTER TABLE gateway_deployments ADD COLUMN IF NOT EXISTS config_hash TEXT;
ALTER TABLE gateway_deployments ADD COLUMN IF NOT EXISTS gateway_data_snapshot TEXT;

-- +goose Down
ALTER TABLE gateway_deployments DROP COLUMN IF EXISTS config_hash;
ALTER TABLE gateway_deployments DROP COLUMN IF EXISTS gateway_data_snapshot;
