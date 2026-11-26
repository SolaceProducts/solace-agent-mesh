-- +goose Up

ALTER TABLE sessions ADD COLUMN last_seq BIGINT NOT NULL DEFAULT 0;

UPDATE sessions s
SET last_seq = agg.max_seq
FROM (
    SELECT app_name, user_id, session_id, MAX(seq) AS max_seq
    FROM session_messages
    GROUP BY app_name, user_id, session_id
) agg
WHERE s.app_name = agg.app_name
  AND s.user_id = agg.user_id
  AND s.session_id = agg.session_id;

-- +goose Down

ALTER TABLE sessions DROP COLUMN last_seq;
