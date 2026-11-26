
-- +goose Up
CREATE TABLE agent_builtin_toolset_configs (
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    toolset_name  TEXT NOT NULL,
    config_values TEXT,
    PRIMARY KEY (agent_id, toolset_name)
);

-- +goose Down
DROP TABLE IF EXISTS agent_builtin_toolset_configs;
