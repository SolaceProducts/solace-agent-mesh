-- +goose Up
CREATE TABLE IF NOT EXISTS volumes (
    id                         TEXT PRIMARY KEY,
    name                       TEXT NOT NULL,
    description                TEXT NOT NULL DEFAULT '',
    user_id                    TEXT NOT NULL,
    creator_type               TEXT NOT NULL,
    creator_name               TEXT NOT NULL DEFAULT '',
    created_at                 INTEGER NOT NULL,
    last_access                INTEGER NOT NULL,
    ttl_seconds                INTEGER,
    expires_at                 INTEGER,
    size_bytes                 INTEGER NOT NULL DEFAULT 0,
    lock_holder                TEXT,
    lock_acquired              INTEGER,
    lock_max_expiry            INTEGER,
    external_path              TEXT,
    external_path_in_container TEXT
);
CREATE INDEX IF NOT EXISTS idx_volumes_user ON volumes (user_id);
CREATE INDEX IF NOT EXISTS idx_volumes_expires ON volumes (expires_at);

CREATE TABLE IF NOT EXISTS session_volumes (
    session_id TEXT NOT NULL,
    volume_id  TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (session_id, volume_id)
);
CREATE INDEX IF NOT EXISTS idx_session_volumes_volume ON session_volumes (volume_id);

CREATE TABLE IF NOT EXISTS volume_quota_guards (
    user_id    TEXT PRIMARY KEY,
    checked_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS volume_quota_guards;
DROP TABLE IF EXISTS session_volumes;
DROP TABLE IF EXISTS volumes;
