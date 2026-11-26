
-- +goose Up
ALTER TABLE gateway_deployments
    ADD COLUMN IF NOT EXISTS failure_reason TEXT,
    ADD COLUMN IF NOT EXISTS error_id TEXT;

-- +goose Down
ALTER TABLE gateway_deployments
    DROP COLUMN IF EXISTS error_id,
    DROP COLUMN IF EXISTS failure_reason;
