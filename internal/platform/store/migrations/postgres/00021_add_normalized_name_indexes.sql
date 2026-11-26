-- +goose Up
CREATE UNIQUE INDEX idx_agents_name_normalized
  ON agents (LOWER(REPLACE(name, ' ', '')));
CREATE UNIQUE INDEX idx_connectors_name_normalized
  ON connectors (LOWER(REPLACE(name, ' ', '')));
CREATE UNIQUE INDEX idx_gateways_name_normalized
  ON gateways (LOWER(REPLACE(name, ' ', '')));
CREATE UNIQUE INDEX idx_a2a_agents_name_normalized
  ON a2a_agents (LOWER(REPLACE(name, ' ', '')));

-- +goose Down
DROP INDEX IF EXISTS idx_a2a_agents_name_normalized;
DROP INDEX IF EXISTS idx_gateways_name_normalized;
DROP INDEX IF EXISTS idx_connectors_name_normalized;
DROP INDEX IF EXISTS idx_agents_name_normalized;
