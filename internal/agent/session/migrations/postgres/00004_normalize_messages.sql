-- +goose Up

CREATE TABLE IF NOT EXISTS session_messages (
    app_name   TEXT    NOT NULL,
    user_id    TEXT    NOT NULL,
    session_id UUID    NOT NULL,
    seq        INTEGER NOT NULL,
    message    TEXT    NOT NULL,
    created_at TEXT    NOT NULL,
    PRIMARY KEY (app_name, user_id, session_id, seq),
    FOREIGN KEY (app_name, user_id, session_id)
        REFERENCES sessions (app_name, user_id, session_id)
        ON DELETE CASCADE
);

INSERT INTO session_messages (app_name, user_id, session_id, seq, message, created_at)
SELECT s.app_name, s.user_id, s.session_id, elem.ord, elem.value::text, s.created_at
FROM sessions s
CROSS JOIN LATERAL jsonb_array_elements(s.messages::jsonb) WITH ORDINALITY AS elem(value, ord)
WHERE jsonb_typeof(s.messages::jsonb) = 'array';

ALTER TABLE sessions DROP COLUMN messages;

-- +goose Down

ALTER TABLE sessions ADD COLUMN messages TEXT NOT NULL DEFAULT '[]';

UPDATE sessions s
SET messages = agg.messages
FROM (
    SELECT app_name, user_id, session_id,
           jsonb_agg(message::jsonb ORDER BY seq)::text AS messages
    FROM session_messages
    GROUP BY app_name, user_id, session_id
) agg
WHERE s.app_name   = agg.app_name
  AND s.user_id    = agg.user_id
  AND s.session_id = agg.session_id;

DROP TABLE session_messages;
