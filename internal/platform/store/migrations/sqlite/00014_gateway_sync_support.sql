-- +goose Up
ALTER TABLE gateway_deployments ADD COLUMN config_hash TEXT;
ALTER TABLE gateway_deployments ADD COLUMN gateway_data_snapshot TEXT;

-- +goose Down
