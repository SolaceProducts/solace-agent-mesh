-- +goose Up

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id      TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (user_id, channel_type)
);

-- +goose Down
DROP TABLE notification_preferences;
