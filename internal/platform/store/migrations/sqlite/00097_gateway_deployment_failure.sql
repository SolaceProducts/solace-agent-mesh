
-- +goose Up
ALTER TABLE gateway_deployments ADD COLUMN failure_reason TEXT;
ALTER TABLE gateway_deployments ADD COLUMN error_id TEXT;

-- +goose Down
ALTER TABLE gateway_deployments DROP COLUMN error_id;
ALTER TABLE gateway_deployments DROP COLUMN failure_reason;
