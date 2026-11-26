-- +goose Up

ALTER TABLE notification_preferences ADD COLUMN destination TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_preferences DROP CONSTRAINT notification_preferences_pkey;
ALTER TABLE notification_preferences ADD PRIMARY KEY (user_id, channel_type, destination);

-- +goose Down
DELETE FROM notification_preferences a
    USING notification_preferences b
    WHERE a.user_id = b.user_id AND a.channel_type = b.channel_type
      AND (a.updated_at < b.updated_at
           OR (a.updated_at = b.updated_at AND a.destination > b.destination));
ALTER TABLE notification_preferences DROP CONSTRAINT notification_preferences_pkey;
ALTER TABLE notification_preferences ADD PRIMARY KEY (user_id, channel_type);
ALTER TABLE notification_preferences DROP COLUMN destination;
