-- +goose Up

CREATE TABLE IF NOT EXISTS gateway_session_keys (
    gateway_type TEXT NOT NULL,
    external_key TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    created_time INTEGER NOT NULL,
    PRIMARY KEY (gateway_type, external_key)
);

CREATE INDEX IF NOT EXISTS idx_gateway_session_keys_session ON gateway_session_keys(session_id);

INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
SELECT
    'slack',
    CASE
        WHEN instr(substr(id, 7), '-') = 0 THEN substr(id, 7) || ':_'
        ELSE substr(id, 7, instr(substr(id, 7), '-') - 1) || ':' ||
             replace(substr(id, 7 + instr(substr(id, 7), '-')), '_', '.')
    END,
    id,
    created_time
FROM sessions
WHERE id LIKE 'slack-%' AND deleted_at IS NULL
ON CONFLICT (gateway_type, external_key) DO NOTHING;

INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
SELECT 'mcp', substr(id, 12), id, created_time
FROM sessions
WHERE id LIKE 'mcp-client-%' AND deleted_at IS NULL
ON CONFLICT (gateway_type, external_key) DO NOTHING;

INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time)
SELECT 'scheduled', substr(id, 11), id, created_time
FROM sessions
WHERE id LIKE 'scheduled\_%' ESCAPE '\' AND deleted_at IS NULL
ON CONFLICT (gateway_type, external_key) DO NOTHING;

-- +goose Down

DROP INDEX IF EXISTS idx_gateway_session_keys_session;
DROP TABLE IF EXISTS gateway_session_keys;
