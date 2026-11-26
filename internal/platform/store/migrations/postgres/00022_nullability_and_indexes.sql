-- +goose Up

UPDATE agents SET description = '' WHERE description IS NULL;
UPDATE connectors SET description = '' WHERE description IS NULL;
UPDATE connectors SET subtype = '' WHERE subtype IS NULL;
UPDATE connectors SET "values" = '{}' WHERE "values" IS NULL;
UPDATE gateways SET description = '' WHERE description IS NULL;
UPDATE gateways SET "values" = '{}' WHERE "values" IS NULL;

ALTER TABLE agents ALTER COLUMN description SET NOT NULL;
ALTER TABLE agents ALTER COLUMN description SET DEFAULT '';
ALTER TABLE connectors ALTER COLUMN description SET NOT NULL;
ALTER TABLE connectors ALTER COLUMN description SET DEFAULT '';
ALTER TABLE connectors ALTER COLUMN subtype SET NOT NULL;
ALTER TABLE connectors ALTER COLUMN subtype SET DEFAULT '';
ALTER TABLE connectors ALTER COLUMN "values" SET NOT NULL;
ALTER TABLE connectors ALTER COLUMN "values" SET DEFAULT '{}';
ALTER TABLE gateways ALTER COLUMN description SET NOT NULL;
ALTER TABLE gateways ALTER COLUMN description SET DEFAULT '';
ALTER TABLE gateways ALTER COLUMN "values" SET NOT NULL;
ALTER TABLE gateways ALTER COLUMN "values" SET DEFAULT '{}';

UPDATE a2a_agents SET agent_auth_type = 'none' WHERE agent_auth_type IS NULL;
UPDATE a2a_agents SET agent_auth_config = '{}' WHERE agent_auth_config IS NULL;
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_type SET NOT NULL;
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_type SET DEFAULT 'none';
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_config SET NOT NULL;
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_config SET DEFAULT '{}';

CREATE UNIQUE INDEX idx_agents_single_a2a_proxy ON agents ((1)) WHERE type = 'a2a_proxy';

CREATE INDEX IF NOT EXISTS ix_model_configurations_auth_type ON model_configurations (model_auth_type);

-- +goose Down
DROP INDEX IF EXISTS ix_model_configurations_auth_type;
DROP INDEX IF EXISTS idx_agents_single_a2a_proxy;

ALTER TABLE a2a_agents ALTER COLUMN agent_auth_config DROP NOT NULL;
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_config DROP DEFAULT;
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_type DROP NOT NULL;
ALTER TABLE a2a_agents ALTER COLUMN agent_auth_type DROP DEFAULT;

ALTER TABLE gateways ALTER COLUMN "values" DROP NOT NULL;
ALTER TABLE gateways ALTER COLUMN "values" DROP DEFAULT;
ALTER TABLE gateways ALTER COLUMN description DROP NOT NULL;
ALTER TABLE gateways ALTER COLUMN description DROP DEFAULT;
ALTER TABLE connectors ALTER COLUMN "values" DROP NOT NULL;
ALTER TABLE connectors ALTER COLUMN "values" DROP DEFAULT;
ALTER TABLE connectors ALTER COLUMN subtype DROP NOT NULL;
ALTER TABLE connectors ALTER COLUMN subtype DROP DEFAULT;
ALTER TABLE connectors ALTER COLUMN description DROP NOT NULL;
ALTER TABLE connectors ALTER COLUMN description DROP DEFAULT;
ALTER TABLE agents ALTER COLUMN description DROP NOT NULL;
ALTER TABLE agents ALTER COLUMN description DROP DEFAULT;
