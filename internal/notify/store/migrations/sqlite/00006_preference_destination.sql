-- +goose Up

CREATE TABLE notification_preferences_new (
    user_id      TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    destination  TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (user_id, channel_type, destination)
);
INSERT INTO notification_preferences_new (user_id, channel_type, destination, enabled, updated_at)
    SELECT user_id, channel_type, '', enabled, updated_at FROM notification_preferences;
DROP TABLE notification_preferences;
ALTER TABLE notification_preferences_new RENAME TO notification_preferences;

-- +goose Down
CREATE TABLE notification_preferences_old (
    user_id      TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (user_id, channel_type)
);
INSERT OR REPLACE INTO notification_preferences_old (user_id, channel_type, enabled, updated_at)
    SELECT user_id, channel_type, enabled, updated_at FROM notification_preferences ORDER BY updated_at ASC;
DROP TABLE notification_preferences;
ALTER TABLE notification_preferences_old RENAME TO notification_preferences;
