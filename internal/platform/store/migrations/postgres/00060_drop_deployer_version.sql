
-- +goose Up
ALTER TABLE agent_deployments DROP COLUMN IF EXISTS deployer_version;
ALTER TABLE gateway_deployments DROP COLUMN IF EXISTS deployer_version;
ALTER TABLE workflow_deployments DROP COLUMN IF EXISTS deployer_version;

-- +goose Down
ALTER TABLE agent_deployments ADD COLUMN IF NOT EXISTS deployer_version TEXT;
ALTER TABLE gateway_deployments ADD COLUMN IF NOT EXISTS deployer_version TEXT;
ALTER TABLE workflow_deployments ADD COLUMN IF NOT EXISTS deployer_version TEXT;
