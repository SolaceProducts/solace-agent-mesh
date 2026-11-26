-- +goose Up

CREATE TABLE sse_event_buffer_new (
    task_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    event_sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    event_data TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    consumed INTEGER NOT NULL DEFAULT 0,
    consumed_at INTEGER,
    PRIMARY KEY (task_id, event_sequence)
);
INSERT INTO sse_event_buffer_new
    SELECT task_id, session_id, user_id, event_sequence, event_type, event_data, created_at, consumed, consumed_at
    FROM sse_event_buffer;
DROP TABLE sse_event_buffer;
ALTER TABLE sse_event_buffer_new RENAME TO sse_event_buffer;
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_id ON sse_event_buffer(task_id);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_session_id ON sse_event_buffer(session_id);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_created_at ON sse_event_buffer(created_at);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_consumed ON sse_event_buffer(consumed);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_consumed ON sse_event_buffer(task_id, consumed);

CREATE TABLE shared_artifacts_new (
    share_id TEXT NOT NULL REFERENCES shared_links(share_id) ON DELETE CASCADE,
    artifact_uri TEXT NOT NULL,
    artifact_version INTEGER,
    is_public INTEGER NOT NULL DEFAULT 1,
    created_time INTEGER NOT NULL,
    PRIMARY KEY (share_id, artifact_uri)
);
INSERT INTO shared_artifacts_new
    SELECT share_id, artifact_uri, artifact_version, is_public, created_time
    FROM shared_artifacts;
DROP TABLE shared_artifacts;
ALTER TABLE shared_artifacts_new RENAME TO shared_artifacts;
CREATE INDEX IF NOT EXISTS idx_shared_artifacts_share_id ON shared_artifacts(share_id);

-- +goose Down

CREATE TABLE sse_event_buffer_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    event_sequence INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    event_data TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    consumed INTEGER NOT NULL DEFAULT 0,
    consumed_at INTEGER,
    UNIQUE(task_id, event_sequence)
);
INSERT INTO sse_event_buffer_old (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at, consumed, consumed_at)
    SELECT task_id, session_id, user_id, event_sequence, event_type, event_data, created_at, consumed, consumed_at
    FROM sse_event_buffer;
DROP TABLE sse_event_buffer;
ALTER TABLE sse_event_buffer_old RENAME TO sse_event_buffer;
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_id ON sse_event_buffer(task_id);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_session_id ON sse_event_buffer(session_id);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_created_at ON sse_event_buffer(created_at);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_consumed ON sse_event_buffer(consumed);
CREATE INDEX IF NOT EXISTS idx_sse_event_buffer_task_consumed ON sse_event_buffer(task_id, consumed);

CREATE TABLE shared_artifacts_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    share_id TEXT NOT NULL REFERENCES shared_links(share_id) ON DELETE CASCADE,
    artifact_uri TEXT NOT NULL,
    artifact_version INTEGER,
    is_public INTEGER NOT NULL DEFAULT 1,
    created_time INTEGER NOT NULL
);
INSERT INTO shared_artifacts_old (share_id, artifact_uri, artifact_version, is_public, created_time)
    SELECT share_id, artifact_uri, artifact_version, is_public, created_time
    FROM shared_artifacts;
DROP TABLE shared_artifacts;
ALTER TABLE shared_artifacts_old RENAME TO shared_artifacts;
CREATE INDEX IF NOT EXISTS idx_shared_artifacts_share_id ON shared_artifacts(share_id);
