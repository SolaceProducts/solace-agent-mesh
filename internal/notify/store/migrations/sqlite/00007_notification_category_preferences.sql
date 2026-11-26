-- +goose Up

CREATE TABLE IF NOT EXISTS notification_category_preferences (
    user_id    TEXT NOT NULL,
    category   TEXT NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, category)
);

-- +goose Down
DROP TABLE notification_category_preferences;
