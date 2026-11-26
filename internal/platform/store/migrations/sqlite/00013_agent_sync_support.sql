-- +goose Up
ALTER TABLE agent_deployments ADD COLUMN config_hash TEXT;
ALTER TABLE agent_deployments ADD COLUMN agent_data_snapshot TEXT;

-- +goose Down
