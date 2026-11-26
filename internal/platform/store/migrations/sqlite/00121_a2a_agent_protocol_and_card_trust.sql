-- +goose Up
ALTER TABLE a2a_agents ADD COLUMN protocol_version TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE a2a_agents ADD COLUMN trusted_card_jwks_urls TEXT;
ALTER TABLE a2a_agents ADD COLUMN require_signed_card INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE a2a_agents DROP COLUMN require_signed_card;
ALTER TABLE a2a_agents DROP COLUMN trusted_card_jwks_urls;
ALTER TABLE a2a_agents DROP COLUMN protocol_version;
