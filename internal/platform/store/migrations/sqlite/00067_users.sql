
-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id              TEXT PRIMARY KEY,
    issuer          TEXT NOT NULL,
    sub             TEXT NOT NULL,
    oidc_provider   TEXT NOT NULL DEFAULT '',
    external_id     TEXT,
    display_name    TEXT,
    user_name       TEXT,
    email           TEXT,
    phone           TEXT,
    source          TEXT NOT NULL DEFAULT '{}',
    last_login_at   INTEGER,
    provisioned_via TEXT NOT NULL DEFAULT 'jit',
    active          INTEGER NOT NULL DEFAULT 1,
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    INTEGER NOT NULL,
    updated_time    INTEGER NOT NULL,
    UNIQUE (issuer, sub)
);

CREATE INDEX IF NOT EXISTS ix_users_email ON users(email);
CREATE INDEX IF NOT EXISTS ix_users_display_name ON users(display_name);
CREATE UNIQUE INDEX IF NOT EXISTS ux_users_phone ON users(phone) WHERE phone IS NOT NULL;

CREATE TABLE IF NOT EXISTS user_groups (
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_value     TEXT NOT NULL,
    PRIMARY KEY (user_id, group_value)
);

CREATE INDEX IF NOT EXISTS ix_user_groups_value ON user_groups(group_value);

-- +goose Down
DROP INDEX IF EXISTS ix_user_groups_value;
DROP TABLE IF EXISTS user_groups;
DROP INDEX IF EXISTS ux_users_phone;
DROP INDEX IF EXISTS ix_users_display_name;
DROP INDEX IF EXISTS ix_users_email;
DROP TABLE IF EXISTS users;
