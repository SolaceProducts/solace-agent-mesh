-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION _to_uuid_sess(val TEXT) RETURNS UUID AS $$
DECLARE
    hex32 TEXT;
BEGIN
    IF val IS NULL THEN RETURN NULL; END IF;

    IF val ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$' THEN
        RETURN val::uuid;
    END IF;

    hex32 := regexp_replace(val, '^(web-session-|scheduled_|teams_|slack-|email-|mcp-client-)', '');

    IF hex32 ~ '^[0-9a-fA-F]{32}$' THEN
        RETURN (
            substring(hex32, 1, 8) || '-' ||
            substring(hex32, 9, 4) || '-' ||
            substring(hex32, 13, 4) || '-' ||
            substring(hex32, 17, 4) || '-' ||
            substring(hex32, 21, 12)
        )::uuid;
    END IF;

    RETURN md5(val)::uuid;
END;
$$ LANGUAGE plpgsql IMMUTABLE;
-- +goose StatementEnd

ALTER TABLE sessions ADD COLUMN context_id TEXT;
ALTER TABLE sessions ADD COLUMN task_id    TEXT;

CREATE TEMP TABLE _session_remap (
    old_id     TEXT PRIMARY KEY,
    new_id     TEXT NOT NULL,
    context_id TEXT,
    task_id    TEXT
);

INSERT INTO _session_remap (old_id, new_id, context_id, task_id)
SELECT
    session_id,
    md5(session_id)::uuid::text,
    CASE
        WHEN session_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}:run$'
            THEN substring(session_id from 1 for 36)
        ELSE NULL
    END,
    CASE
        WHEN session_id ~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}:run$'
            THEN substring(session_id from 38 for 36)
        ELSE NULL
    END
FROM sessions
WHERE session_id LIKE '%:run'
ON CONFLICT (old_id) DO NOTHING;

CREATE TEMP TABLE _default_remap (
    app_name TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    new_id   TEXT NOT NULL,
    PRIMARY KEY (app_name, user_id)
);

INSERT INTO _default_remap (app_name, user_id, new_id)
SELECT DISTINCT
    app_name,
    user_id,
    md5(app_name || ':' || user_id || ':default')::uuid::text
FROM sessions
WHERE session_id = 'default'
ON CONFLICT (app_name, user_id) DO NOTHING;

UPDATE sessions s
SET session_id = r.new_id,
    context_id = COALESCE(r.context_id, r.new_id),
    task_id    = r.task_id
FROM _session_remap r
WHERE s.session_id = r.old_id;

UPDATE task_checkpoints tc
SET session_id = r.new_id
FROM _session_remap r
WHERE tc.session_id = r.old_id;

UPDATE sessions s
SET session_id = d.new_id,
    context_id = d.new_id
FROM _default_remap d
WHERE s.session_id = 'default'
  AND s.app_name   = d.app_name
  AND s.user_id    = d.user_id;

UPDATE task_checkpoints tc
SET session_id = d.new_id
FROM _default_remap d
WHERE tc.session_id = 'default'
  AND tc.app_name   = d.app_name
  AND tc.user_id    = d.user_id;

DROP TABLE _session_remap;
DROP TABLE _default_remap;

UPDATE sessions SET session_id = _to_uuid_sess(session_id)::text
WHERE session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE task_checkpoints SET session_id = _to_uuid_sess(session_id)::text
WHERE session_id !~ '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-';

UPDATE sessions SET context_id = session_id WHERE context_id IS NULL;

DELETE FROM task_checkpoints tc
WHERE NOT EXISTS (
    SELECT 1 FROM sessions s
    WHERE s.app_name = tc.app_name
      AND s.user_id  = tc.user_id
      AND s.session_id = tc.session_id
);

ALTER TABLE sessions ALTER COLUMN session_id TYPE UUID USING session_id::uuid;
ALTER TABLE sessions ALTER COLUMN context_id TYPE UUID USING context_id::uuid;
ALTER TABLE sessions ALTER COLUMN task_id    TYPE UUID USING task_id::uuid;
ALTER TABLE task_checkpoints ALTER COLUMN session_id TYPE UUID USING session_id::uuid;

ALTER TABLE sessions ALTER COLUMN context_id SET NOT NULL;

CREATE UNIQUE INDEX idx_sessions_context_task
    ON sessions(app_name, user_id, context_id, task_id)
    WHERE task_id IS NOT NULL;

CREATE INDEX idx_sessions_context
    ON sessions(app_name, user_id, context_id);

ALTER TABLE task_checkpoints
    ADD CONSTRAINT task_checkpoints_session_id_fkey
    FOREIGN KEY (app_name, user_id, session_id)
    REFERENCES sessions(app_name, user_id, session_id)
    ON DELETE CASCADE;

DROP FUNCTION _to_uuid_sess(TEXT);

-- +goose Down

ALTER TABLE task_checkpoints DROP CONSTRAINT IF EXISTS task_checkpoints_session_id_fkey;
DROP INDEX IF EXISTS idx_sessions_context;
DROP INDEX IF EXISTS idx_sessions_context_task;

ALTER TABLE sessions ALTER COLUMN context_id DROP NOT NULL;
ALTER TABLE sessions ALTER COLUMN session_id TYPE TEXT USING session_id::text;
ALTER TABLE sessions ALTER COLUMN context_id TYPE TEXT USING context_id::text;
ALTER TABLE sessions ALTER COLUMN task_id    TYPE TEXT USING task_id::text;
ALTER TABLE task_checkpoints ALTER COLUMN session_id TYPE TEXT USING session_id::text;

ALTER TABLE sessions DROP COLUMN context_id;
ALTER TABLE sessions DROP COLUMN task_id;
