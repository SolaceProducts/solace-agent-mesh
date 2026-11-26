-- +goose Up

CREATE TABLE gateway_session_keys (
    gateway_type TEXT NOT NULL,
    external_key TEXT NOT NULL,
    session_id   UUID NOT NULL,
    created_time BIGINT NOT NULL,
    PRIMARY KEY (gateway_type, external_key)
);

CREATE INDEX idx_gateway_session_keys_session ON gateway_session_keys(session_id);

INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
SELECT
    'scheduled',
    e.id,
    md5('scheduled_' || e.id)::uuid,
    COALESCE(s.created_time, (EXTRACT(EPOCH FROM now()) * 1000)::bigint)
FROM scheduled_task_executions e
JOIN sessions s
    ON s.id = md5('scheduled_' || e.id)::uuid
   AND s.deleted_at IS NULL
ON CONFLICT (gateway_type, external_key) DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS gateway_session_keys;
