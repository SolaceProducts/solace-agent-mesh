-- +goose Up

CREATE TABLE IF NOT EXISTS notify_user_channels (
    user_id      TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    address      TEXT NOT NULL,
    email        TEXT NOT NULL DEFAULT '',
    gateway_id   TEXT NOT NULL DEFAULT '',
    updated_at   BIGINT NOT NULL,
    PRIMARY KEY (user_id, channel_type)
);

CREATE INDEX IF NOT EXISTS idx_notify_user_channels_email
    ON notify_user_channels (email);

-- +goose Down
DROP TABLE notify_user_channels;
