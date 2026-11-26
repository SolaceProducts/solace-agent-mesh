-- +goose Up

CREATE TABLE IF NOT EXISTS session_messages (
    app_name   TEXT    NOT NULL,
    user_id    TEXT    NOT NULL,
    session_id TEXT    NOT NULL,
    seq        INTEGER NOT NULL,
    message    TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    PRIMARY KEY (app_name, user_id, session_id, seq),
    FOREIGN KEY (app_name, user_id, session_id)
        REFERENCES sessions (app_name, user_id, session_id)
        ON DELETE CASCADE
);

INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at)
SELECT s.app_name, s.user_id, s.session_id, je.key + 1, je.value, s.created_at
FROM sessions s, json_each(s.messages) AS je
WHERE COALESCE(json_valid(s.messages), 0) = 1
  AND json_type(s.messages) = 'array';

ALTER TABLE sessions DROP COLUMN messages;

-- +goose Down

ALTER TABLE sessions ADD COLUMN messages TEXT NOT NULL DEFAULT '[]';

UPDATE sessions
SET messages = agg.messages
FROM (
    SELECT app_name, user_id, session_id,
           json_group_array(json(message) ORDER BY seq) AS messages
    FROM session_messages
    GROUP BY app_name, user_id, session_id
) AS agg
WHERE sessions.app_name   = agg.app_name
  AND sessions.user_id    = agg.user_id
  AND sessions.session_id = agg.session_id;

DROP TABLE session_messages;
