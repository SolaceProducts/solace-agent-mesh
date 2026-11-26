-- +goose Up

CREATE TABLE tool_packages (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    description     TEXT,
    runtime         TEXT,
    discovery_status TEXT NOT NULL DEFAULT 'created',
    discovery_errors TEXT,
    zip_resource_key TEXT,
    zip_filename     TEXT,
    zip_file_size    BIGINT,
    created_by      TEXT NOT NULL DEFAULT '',
    updated_by      TEXT NOT NULL DEFAULT '',
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL
);

CREATE TABLE tool_package_files (
    id               TEXT PRIMARY KEY,
    package_id       TEXT NOT NULL REFERENCES tool_packages(id) ON DELETE CASCADE,
    tool_name        TEXT NOT NULL,
    tool_description TEXT,
    timeout_seconds  INTEGER NOT NULL DEFAULT 300,
    sandbox_profile  TEXT NOT NULL DEFAULT 'standard',
    parameters       TEXT,
    config_schema    TEXT,
    created_time     BIGINT NOT NULL,
    UNIQUE(package_id, tool_name)
);

CREATE TABLE agent_toolset_configs (
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    package_id    TEXT NOT NULL REFERENCES tool_packages(id) ON DELETE CASCADE,
    config_values TEXT,
    PRIMARY KEY (agent_id, package_id)
);

CREATE INDEX idx_tool_package_files_package_id ON tool_package_files(package_id);
CREATE INDEX idx_tool_packages_discovery_status ON tool_packages(discovery_status);

-- +goose Down

DROP TABLE IF EXISTS agent_toolset_configs;
DROP TABLE IF EXISTS tool_package_files;
DROP TABLE IF EXISTS tool_packages;
