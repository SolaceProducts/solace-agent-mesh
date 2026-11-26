-- +goose Up

ALTER TABLE sessions ADD COLUMN last_seq INTEGER NOT NULL DEFAULT 0;

UPDATE sessions
SET last_seq = agg.max_seq
FROM (
    SELECT app_name, user_id, session_id, MAX(seq) AS max_seq
    FROM session_messages
    GROUP BY app_name, user_id, session_id
) AS agg
WHERE sessions.app_name = agg.app_name
  AND sessions.user_id = agg.user_id
  AND sessions.session_id = agg.session_id;

-- +goose Down

ALTER TABLE sessions DROP COLUMN last_seq;
