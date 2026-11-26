
-- +goose Up
ALTER TABLE agent_deployments
    ADD COLUMN IF NOT EXISTS failure_reason TEXT,
    ADD COLUMN IF NOT EXISTS error_id TEXT;
ALTER TABLE workflow_deployments
    ADD COLUMN IF NOT EXISTS failure_reason TEXT,
    ADD COLUMN IF NOT EXISTS error_id TEXT;

-- +goose Down
ALTER TABLE workflow_deployments
    DROP COLUMN IF EXISTS error_id,
    DROP COLUMN IF EXISTS failure_reason;
ALTER TABLE agent_deployments
    DROP COLUMN IF EXISTS error_id,
    DROP COLUMN IF EXISTS failure_reason;
