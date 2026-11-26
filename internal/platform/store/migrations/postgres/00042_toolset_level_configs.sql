
-- +goose Up
CREATE TABLE toolset_configs (
    package_id    UUID PRIMARY KEY REFERENCES tool_packages(id) ON DELETE CASCADE,
    config_values TEXT,
    updated_by    TEXT NOT NULL DEFAULT '',
    updated_time  BIGINT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS toolset_configs;
