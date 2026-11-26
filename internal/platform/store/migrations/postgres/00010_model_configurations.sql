-- +goose Up
CREATE TABLE IF NOT EXISTS model_configurations (
    id              UUID PRIMARY KEY,
    alias           TEXT NOT NULL UNIQUE,
    provider        TEXT NOT NULL,
    model_name      TEXT NOT NULL,
    api_base        TEXT,
    model_auth_type TEXT NOT NULL DEFAULT 'none',
    model_auth_config TEXT NOT NULL DEFAULT '{}',
    model_params    TEXT NOT NULL DEFAULT '{}',
    description     TEXT,
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS ix_model_configurations_alias ON model_configurations(alias);
CREATE INDEX IF NOT EXISTS ix_model_configurations_provider ON model_configurations(provider);

-- +goose Down
DROP INDEX IF EXISTS ix_model_configurations_provider;
DROP INDEX IF EXISTS ix_model_configurations_alias;
DROP TABLE IF EXISTS model_configurations;
