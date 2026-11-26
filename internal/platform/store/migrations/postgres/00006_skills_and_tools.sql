-- +goose Up

CREATE TABLE IF NOT EXISTS skills (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    tags TEXT,
    origin TEXT NOT NULL DEFAULT 'filesystem',
    status TEXT NOT NULL DEFAULT 'available',
    has_references INTEGER NOT NULL DEFAULT 0,
    has_assets INTEGER NOT NULL DEFAULT 0,
    has_tools INTEGER NOT NULL DEFAULT 0,
    resource_count INTEGER NOT NULL DEFAULT 0,
    bundled_tools TEXT,
    config_yaml TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS tools (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    origin TEXT NOT NULL DEFAULT 'filesystem',
    status TEXT NOT NULL DEFAULT 'available',
    parameters TEXT,
    instructions TEXT,
    artifact_params TEXT,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS tools;
DROP TABLE IF EXISTS skills;
