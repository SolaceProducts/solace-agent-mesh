
-- +goose Up
ALTER TABLE agent_builtin_toolset_configs ADD COLUMN volume_bindings TEXT;

-- +goose Down
ALTER TABLE agent_builtin_toolset_configs DROP COLUMN volume_bindings;
