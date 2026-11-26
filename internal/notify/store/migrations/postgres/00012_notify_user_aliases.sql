-- +goose Up

CREATE TABLE IF NOT EXISTS notify_user_aliases (
    login_id         TEXT PRIMARY KEY,
    platform_user_id TEXT NOT NULL,
    updated_at       BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_notify_user_aliases_platform_user
    ON notify_user_aliases (platform_user_id);

-- +goose Down
DROP TABLE notify_user_aliases;
