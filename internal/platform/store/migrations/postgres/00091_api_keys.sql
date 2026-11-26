
-- +goose Up
CREATE TABLE IF NOT EXISTS api_keys (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label           TEXT NOT NULL,
    key_hash        TEXT NOT NULL,
    prefix          TEXT NOT NULL,
    expires_time    BIGINT,
    last_used_time  BIGINT,
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_api_keys_key_hash ON api_keys(key_hash);

CREATE INDEX IF NOT EXISTS ix_api_keys_user_id ON api_keys(user_id);

CREATE TABLE IF NOT EXISTS api_key_scopes (
    api_key_id      UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    scope           TEXT NOT NULL,
    PRIMARY KEY (api_key_id, scope)
);

-- +goose Down
DROP TABLE IF EXISTS api_key_scopes;
DROP INDEX IF EXISTS ix_api_keys_user_id;
DROP INDEX IF EXISTS ux_api_keys_key_hash;
DROP TABLE IF EXISTS api_keys;
