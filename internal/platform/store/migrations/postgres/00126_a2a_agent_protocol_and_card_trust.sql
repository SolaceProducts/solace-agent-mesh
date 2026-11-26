-- +goose Up
ALTER TABLE a2a_agents ADD COLUMN IF NOT EXISTS protocol_version TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE a2a_agents ADD COLUMN IF NOT EXISTS trusted_card_jwks_urls TEXT;
ALTER TABLE a2a_agents ADD COLUMN IF NOT EXISTS require_signed_card INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE a2a_agents DROP COLUMN IF EXISTS require_signed_card;
ALTER TABLE a2a_agents DROP COLUMN IF EXISTS trusted_card_jwks_urls;
ALTER TABLE a2a_agents DROP COLUMN IF EXISTS protocol_version;
