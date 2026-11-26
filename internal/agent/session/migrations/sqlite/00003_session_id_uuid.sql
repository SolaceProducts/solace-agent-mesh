-- +goose Up

ALTER TABLE sessions ADD COLUMN context_id TEXT;
ALTER TABLE sessions ADD COLUMN task_id    TEXT;

CREATE TEMPORARY TABLE _session_remap (
    old_id     TEXT PRIMARY KEY,
    new_id     TEXT NOT NULL,
    context_id TEXT,
    task_id    TEXT
);

INSERT OR IGNORE INTO _session_remap (old_id, new_id, context_id, task_id)
SELECT
    session_id,
    lower(hex(randomblob(4))) || '-' ||
    lower(hex(randomblob(2))) || '-' ||
    lower(hex(randomblob(2))) || '-' ||
    lower(hex(randomblob(2))) || '-' ||
    lower(hex(randomblob(6))),
    CASE
        WHEN length(session_id) = 77
         AND substr(session_id, 9, 1)  = '-'
         AND substr(session_id, 14, 1) = '-'
         AND substr(session_id, 19, 1) = '-'
         AND substr(session_id, 24, 1) = '-'
         AND substr(session_id, 37, 1) = ':'
         AND substr(session_id, 46, 1) = '-'
         AND substr(session_id, 51, 1) = '-'
         AND substr(session_id, 56, 1) = '-'
         AND substr(session_id, 61, 1) = '-'
         AND substr(session_id, 74, 4) = ':run'
        THEN substr(session_id, 1, 36)
        ELSE NULL
    END,
    CASE
        WHEN length(session_id) = 77
         AND substr(session_id, 37, 1) = ':'
         AND substr(session_id, 74, 4) = ':run'
        THEN substr(session_id, 38, 36)
        ELSE NULL
    END
FROM sessions
WHERE session_id LIKE '%:run';

CREATE TEMPORARY TABLE _default_remap (
    app_name TEXT NOT NULL,
    user_id  TEXT NOT NULL,
    new_id   TEXT NOT NULL,
    PRIMARY KEY (app_name, user_id)
);

INSERT OR IGNORE INTO _default_remap (app_name, user_id, new_id)
SELECT
    app_name,
    user_id,
    lower(hex(randomblob(4))) || '-' ||
    lower(hex(randomblob(2))) || '-' ||
    lower(hex(randomblob(2))) || '-' ||
    lower(hex(randomblob(2))) || '-' ||
    lower(hex(randomblob(6)))
FROM sessions
WHERE session_id = 'default';

UPDATE sessions
SET session_id = (SELECT new_id FROM _session_remap WHERE old_id = sessions.session_id),
    context_id = COALESCE(
        (SELECT context_id FROM _session_remap WHERE old_id = sessions.session_id),
        (SELECT new_id     FROM _session_remap WHERE old_id = sessions.session_id)
    ),
    task_id    = (SELECT task_id FROM _session_remap WHERE old_id = sessions.session_id)
WHERE session_id IN (SELECT old_id FROM _session_remap);

UPDATE task_checkpoints
SET session_id = (SELECT new_id FROM _session_remap WHERE old_id = task_checkpoints.session_id)
WHERE session_id IN (SELECT old_id FROM _session_remap);

UPDATE sessions
SET session_id = (
        SELECT new_id FROM _default_remap
        WHERE app_name = sessions.app_name AND user_id = sessions.user_id
    ),
    context_id = (
        SELECT new_id FROM _default_remap
        WHERE app_name = sessions.app_name AND user_id = sessions.user_id
    )
WHERE session_id = 'default';

UPDATE task_checkpoints
SET session_id = (
    SELECT new_id FROM _default_remap
    WHERE app_name = task_checkpoints.app_name AND user_id = task_checkpoints.user_id
)
WHERE session_id = 'default';

DROP TABLE _session_remap;
DROP TABLE _default_remap;

UPDATE sessions SET context_id = session_id WHERE context_id IS NULL;

DELETE FROM task_checkpoints
WHERE NOT EXISTS (
    SELECT 1 FROM sessions s
    WHERE s.app_name = task_checkpoints.app_name
      AND s.user_id  = task_checkpoints.user_id
      AND s.session_id = task_checkpoints.session_id
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_context_task
    ON sessions(app_name, user_id, context_id, task_id)
    WHERE task_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_context
    ON sessions(app_name, user_id, context_id);

-- +goose Down

DROP INDEX IF EXISTS idx_sessions_context;
DROP INDEX IF EXISTS idx_sessions_context_task;
ALTER TABLE sessions DROP COLUMN context_id;
ALTER TABLE sessions DROP COLUMN task_id;
