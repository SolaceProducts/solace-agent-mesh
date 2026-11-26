-- +goose Up
ALTER TABLE agent_deployments ADD COLUMN deployed_payloads TEXT;

-- +goose Down
ALTER TABLE agent_deployments DROP COLUMN deployed_payloads;
