
-- +goose Up
ALTER TABLE agent_deployments ADD COLUMN prompt_hash TEXT;
ALTER TABLE agent_deployments ADD COLUMN toolset_hash TEXT;
ALTER TABLE agent_deployments ADD COLUMN capability_hash TEXT;
ALTER TABLE agent_deployments ADD COLUMN behaviour_hash TEXT;
ALTER TABLE agent_deployments ADD COLUMN card_hash TEXT;

-- +goose Down
ALTER TABLE agent_deployments DROP COLUMN card_hash;
ALTER TABLE agent_deployments DROP COLUMN behaviour_hash;
ALTER TABLE agent_deployments DROP COLUMN capability_hash;
ALTER TABLE agent_deployments DROP COLUMN toolset_hash;
ALTER TABLE agent_deployments DROP COLUMN prompt_hash;
