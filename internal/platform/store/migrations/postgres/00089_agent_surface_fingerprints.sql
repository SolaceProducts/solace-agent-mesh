
-- +goose Up
ALTER TABLE agent_deployments
    ADD COLUMN IF NOT EXISTS prompt_hash     TEXT,
    ADD COLUMN IF NOT EXISTS toolset_hash    TEXT,
    ADD COLUMN IF NOT EXISTS capability_hash TEXT,
    ADD COLUMN IF NOT EXISTS behaviour_hash  TEXT,
    ADD COLUMN IF NOT EXISTS card_hash       TEXT;

-- +goose Down
ALTER TABLE agent_deployments
    DROP COLUMN IF EXISTS card_hash,
    DROP COLUMN IF EXISTS behaviour_hash,
    DROP COLUMN IF EXISTS capability_hash,
    DROP COLUMN IF EXISTS toolset_hash,
    DROP COLUMN IF EXISTS prompt_hash;
