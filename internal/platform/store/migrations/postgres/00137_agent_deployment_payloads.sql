-- +goose Up
ALTER TABLE agent_deployments ADD COLUMN IF NOT EXISTS deployed_payloads TEXT;

-- +goose Down
ALTER TABLE agent_deployments DROP COLUMN IF EXISTS deployed_payloads;
