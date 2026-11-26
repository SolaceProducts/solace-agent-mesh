
-- +goose Up
ALTER TABLE gateway_deployments
    ADD COLUMN IF NOT EXISTS prompt_transform_hash TEXT;

-- +goose Down
ALTER TABLE gateway_deployments
    DROP COLUMN IF EXISTS prompt_transform_hash;
