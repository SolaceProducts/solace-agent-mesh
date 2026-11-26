
-- +goose Up
ALTER TABLE gateway_deployments ADD COLUMN prompt_transform_hash TEXT;

-- +goose Down
ALTER TABLE gateway_deployments DROP COLUMN prompt_transform_hash;
