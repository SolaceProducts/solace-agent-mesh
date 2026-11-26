
-- +goose Up
ALTER TABLE agent_builtin_toolset_configs ADD COLUMN IF NOT EXISTS network_bindings TEXT;

-- +goose Down
ALTER TABLE agent_builtin_toolset_configs DROP COLUMN IF EXISTS network_bindings;
