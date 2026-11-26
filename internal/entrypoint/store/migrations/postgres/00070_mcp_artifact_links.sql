-- +goose Up

CREATE TABLE IF NOT EXISTS mcp_artifact_links (
    mcp_session_id TEXT NOT NULL,
    uri            TEXT NOT NULL,
    agent_name     TEXT NOT NULL,
    user_id        TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    filename       TEXT NOT NULL,
    display_name   TEXT NOT NULL DEFAULT '',
    mime_type      TEXT NOT NULL DEFAULT '',
    version        INTEGER,
    created_at     BIGINT NOT NULL,
    PRIMARY KEY (mcp_session_id, uri)
);

CREATE INDEX IF NOT EXISTS idx_mcp_artifact_links_session_id
    ON mcp_artifact_links (session_id);

CREATE INDEX IF NOT EXISTS idx_mcp_artifact_links_created_at
    ON mcp_artifact_links (created_at);

-- +goose Down

DROP INDEX IF EXISTS idx_mcp_artifact_links_created_at;
DROP INDEX IF EXISTS idx_mcp_artifact_links_session_id;
DROP TABLE IF EXISTS mcp_artifact_links;
