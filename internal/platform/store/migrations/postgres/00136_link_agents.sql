-- +goose Up
ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy', 'link'));

CREATE TABLE IF NOT EXISTS link_agents (
    id UUID PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    entrypoint_id UUID NOT NULL REFERENCES gateways(id),
    token_sha256 TEXT NOT NULL,
    token_prefix TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    owner TEXT NOT NULL DEFAULT '',
    team TEXT NOT NULL DEFAULT '',
    peers TEXT NOT NULL DEFAULT '[]',
    share_user_profile INTEGER NOT NULL DEFAULT 0,
    wake TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_link_agents_entrypoint ON link_agents (entrypoint_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_link_agents_token_sha256 ON link_agents (token_sha256);

-- +goose Down
DROP INDEX IF EXISTS idx_link_agents_token_sha256;
DROP INDEX IF EXISTS idx_link_agents_entrypoint;
DROP TABLE IF EXISTS link_agents;
DELETE FROM agents WHERE type = 'link';
ALTER TABLE agents DROP CONSTRAINT IF EXISTS check_agent_type;
ALTER TABLE agents ADD CONSTRAINT check_agent_type
  CHECK (type IN ('standard', 'a2a_proxy'));
