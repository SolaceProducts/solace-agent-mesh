
-- +goose Up
ALTER TABLE agent_builtin_toolset_configs ADD COLUMN IF NOT EXISTS volume_bindings TEXT;

-- +goose Down
ALTER TABLE agent_builtin_toolset_configs DROP COLUMN IF EXISTS volume_bindings;
