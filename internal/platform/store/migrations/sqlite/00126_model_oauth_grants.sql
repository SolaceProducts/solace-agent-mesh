
-- +goose Up
CREATE TABLE IF NOT EXISTS model_oauth_grants (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    flow TEXT NOT NULL,
    issuer TEXT NOT NULL,
    token_endpoint TEXT NOT NULL,
    client_id TEXT NOT NULL,
    audience TEXT NOT NULL DEFAULT '',
    scopes TEXT NOT NULL DEFAULT '',
    account_subject TEXT NOT NULL DEFAULT '',
    account_label TEXT NOT NULL DEFAULT '',
    refresh_token TEXT NOT NULL,
    access_token TEXT NOT NULL DEFAULT '',
    access_token_expires_time BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    refresh_started_time BIGINT NOT NULL DEFAULT 0,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS model_oauth_grants;
