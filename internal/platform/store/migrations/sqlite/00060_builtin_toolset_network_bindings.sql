
-- +goose Up
ALTER TABLE agent_builtin_toolset_configs ADD COLUMN network_bindings TEXT;

-- +goose Down
ALTER TABLE agent_builtin_toolset_configs DROP COLUMN network_bindings;
