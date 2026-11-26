-- +goose Up

CREATE TABLE IF NOT EXISTS notification_cell_preferences (
    user_id      TEXT NOT NULL,
    category     TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    destination  TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    updated_at   BIGINT NOT NULL,
    PRIMARY KEY (user_id, category, channel_type, destination)
);

-- +goose Down
DROP TABLE notification_cell_preferences;
