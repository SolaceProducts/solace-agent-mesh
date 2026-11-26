-- +goose Up

ALTER TABLE agents ADD COLUMN is_ephemeral INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN expires_at INTEGER;
ALTER TABLE agents ADD COLUMN source_session_id TEXT;
ALTER TABLE agents ADD COLUMN source_component_name TEXT;

CREATE INDEX IF NOT EXISTS idx_agents_ephemeral_expires ON agents(is_ephemeral, expires_at)
    WHERE is_ephemeral = 1;

CREATE INDEX IF NOT EXISTS idx_agents_session_owner ON agents(source_session_id, created_by)
    WHERE is_ephemeral = 1;

-- +goose Down

DROP INDEX IF EXISTS idx_agents_session_owner;
DROP INDEX IF EXISTS idx_agents_ephemeral_expires;
ALTER TABLE agents DROP COLUMN source_component_name;
ALTER TABLE agents DROP COLUMN source_session_id;
ALTER TABLE agents DROP COLUMN expires_at;
ALTER TABLE agents DROP COLUMN is_ephemeral;
