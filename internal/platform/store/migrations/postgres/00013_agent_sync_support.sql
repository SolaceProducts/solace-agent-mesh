-- +goose Up
ALTER TABLE agent_deployments ADD COLUMN IF NOT EXISTS config_hash TEXT;
ALTER TABLE agent_deployments ADD COLUMN IF NOT EXISTS agent_data_snapshot TEXT;

-- +goose Down
ALTER TABLE agent_deployments DROP COLUMN IF EXISTS config_hash;
ALTER TABLE agent_deployments DROP COLUMN IF EXISTS agent_data_snapshot;
