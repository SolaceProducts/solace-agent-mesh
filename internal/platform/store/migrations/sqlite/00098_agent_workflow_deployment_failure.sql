
-- +goose Up
ALTER TABLE agent_deployments ADD COLUMN failure_reason TEXT;
ALTER TABLE agent_deployments ADD COLUMN error_id TEXT;
ALTER TABLE workflow_deployments ADD COLUMN failure_reason TEXT;
ALTER TABLE workflow_deployments ADD COLUMN error_id TEXT;

-- +goose Down
ALTER TABLE workflow_deployments DROP COLUMN error_id;
ALTER TABLE workflow_deployments DROP COLUMN failure_reason;
ALTER TABLE agent_deployments DROP COLUMN error_id;
ALTER TABLE agent_deployments DROP COLUMN failure_reason;
