
-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_connector_oauth_states (
    state_hash    TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL,
    owner_user_id TEXT NOT NULL,
    data          TEXT NOT NULL,
    access_token  TEXT NOT NULL DEFAULT '',
    expires_at    BIGINT NOT NULL,
    created_time  BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_connector_oauth_states_session ON mcp_connector_oauth_states(session_id);
CREATE INDEX IF NOT EXISTS idx_mcp_connector_oauth_states_expires ON mcp_connector_oauth_states(expires_at);

-- +goose Down
DROP TABLE IF EXISTS mcp_connector_oauth_states;
