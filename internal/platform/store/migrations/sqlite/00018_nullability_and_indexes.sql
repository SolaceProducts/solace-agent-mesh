-- +goose Up

UPDATE agents SET description = '' WHERE description IS NULL;
UPDATE connectors SET description = '' WHERE description IS NULL;
UPDATE connectors SET subtype = '' WHERE subtype IS NULL;
UPDATE connectors SET "values" = '{}' WHERE "values" IS NULL;
UPDATE gateways SET description = '' WHERE description IS NULL;
UPDATE gateways SET "values" = '{}' WHERE "values" IS NULL;
UPDATE a2a_agents SET agent_auth_type = 'none' WHERE agent_auth_type IS NULL;
UPDATE a2a_agents SET agent_auth_config = '{}' WHERE agent_auth_config IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_single_a2a_proxy ON agents (type) WHERE type = 'a2a_proxy';

CREATE INDEX IF NOT EXISTS ix_model_configurations_auth_type ON model_configurations (model_auth_type);

-- +goose Down
DROP INDEX IF EXISTS ix_model_configurations_auth_type;
DROP INDEX IF EXISTS idx_agents_single_a2a_proxy;
