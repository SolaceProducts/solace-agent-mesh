-- +goose Up
CREATE TABLE IF NOT EXISTS shared_links (
    share_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    title TEXT,
    is_public INTEGER NOT NULL DEFAULT 1,
    require_authentication INTEGER NOT NULL DEFAULT 0,
    allowed_domains TEXT,
    created_time INTEGER NOT NULL,
    updated_time INTEGER NOT NULL,
    deleted_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_shared_links_session_id ON shared_links(session_id);
CREATE INDEX IF NOT EXISTS idx_shared_links_user_id ON shared_links(user_id);
CREATE INDEX IF NOT EXISTS idx_shared_links_created_time ON shared_links(created_time);
CREATE INDEX IF NOT EXISTS idx_shared_links_deleted_at ON shared_links(deleted_at);
CREATE INDEX IF NOT EXISTS idx_shared_links_require_auth ON shared_links(require_authentication);

CREATE TABLE IF NOT EXISTS shared_link_users (
    id TEXT PRIMARY KEY,
    share_id TEXT NOT NULL REFERENCES shared_links(share_id) ON DELETE CASCADE,
    user_email TEXT NOT NULL,
    access_level TEXT NOT NULL DEFAULT 'RESOURCE_VIEWER',
    added_at INTEGER NOT NULL,
    added_by_user_id TEXT NOT NULL,
    original_access_level TEXT,
    original_added_at INTEGER,
    UNIQUE(share_id, user_email)
);
CREATE INDEX IF NOT EXISTS idx_shared_link_users_share_id ON shared_link_users(share_id);
CREATE INDEX IF NOT EXISTS idx_shared_link_users_user_email ON shared_link_users(user_email);

CREATE TABLE IF NOT EXISTS shared_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    share_id TEXT NOT NULL REFERENCES shared_links(share_id) ON DELETE CASCADE,
    artifact_uri TEXT NOT NULL,
    artifact_version INTEGER,
    is_public INTEGER NOT NULL DEFAULT 1,
    created_time INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_shared_artifacts_share_id ON shared_artifacts(share_id);

-- +goose Down
DROP TABLE IF EXISTS shared_artifacts;
DROP TABLE IF EXISTS shared_link_users;
DROP TABLE IF EXISTS shared_links;
