-- +goose Up
DROP INDEX IF EXISTS idx_agents_single_a2a_proxy;

-- +goose Down
CREATE UNIQUE INDEX IF NOT EXISTS idx_agents_single_a2a_proxy ON agents (type) WHERE type = 'a2a_proxy';
