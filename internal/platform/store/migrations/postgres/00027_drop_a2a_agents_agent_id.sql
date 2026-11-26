-- +goose Up
ALTER TABLE a2a_agents DROP COLUMN IF EXISTS agent_id;

-- +goose Down
ALTER TABLE a2a_agents ADD COLUMN agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL;
