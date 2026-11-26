-- +goose Up

CREATE TABLE IF NOT EXISTS workflows (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    display_name TEXT,
    description TEXT,
    app_config TEXT NOT NULL,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_deployments (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    action TEXT NOT NULL,
    workflow_config_snapshot TEXT,
    error_message TEXT,
    deployer_version TEXT,
    created_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS workflow_deployments;
DROP TABLE IF EXISTS workflows;
