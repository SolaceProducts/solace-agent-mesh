
-- +goose Up
CREATE TABLE IF NOT EXISTS mcp_oauth_state (
    state_hash    TEXT PRIMARY KEY,
    gateway_id    TEXT NOT NULL,
    verifier_hash TEXT NOT NULL,
    data          TEXT NOT NULL,
    expires_at    INTEGER NOT NULL,
    created_time  INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_state_verifier ON mcp_oauth_state(verifier_hash);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_state_gateway_expires ON mcp_oauth_state(gateway_id, expires_at);

CREATE TABLE IF NOT EXISTS mcp_oauth_code (
    code_hash    TEXT PRIMARY KEY,
    gateway_id   TEXT NOT NULL,
    data         TEXT NOT NULL,
    expires_at   INTEGER NOT NULL,
    created_time INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_code_expires ON mcp_oauth_code(expires_at);

CREATE TABLE IF NOT EXISTS mcp_oauth_refresh (
    token_hash   TEXT PRIMARY KEY,
    gateway_id   TEXT NOT NULL,
    data         TEXT NOT NULL,
    expires_at   INTEGER NOT NULL,
    created_time INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mcp_oauth_refresh_expires ON mcp_oauth_refresh(expires_at);

-- +goose Down
DROP TABLE IF EXISTS mcp_oauth_refresh;
DROP TABLE IF EXISTS mcp_oauth_code;
DROP TABLE IF EXISTS mcp_oauth_state;
