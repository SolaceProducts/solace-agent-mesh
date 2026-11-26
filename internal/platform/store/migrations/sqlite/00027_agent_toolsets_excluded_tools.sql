-- +goose Up
ALTER TABLE agent_toolsets ADD COLUMN excluded_tools TEXT;

-- +goose Down
ALTER TABLE agent_toolsets DROP COLUMN excluded_tools;
