-- +goose Up
CREATE TABLE IF NOT EXISTS system_user_tool_auth (
    principal TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    credential_key TEXT NOT NULL,
    state TEXT NOT NULL,
    begun_by TEXT,
    begun_time BIGINT,
    begun_ref TEXT,
    authorized_by TEXT,
    authorized_time BIGINT,
    connected_observed_time BIGINT,
    disconnected_by TEXT,
    disconnected_time BIGINT,
    PRIMARY KEY (principal, agent_name, credential_key)
);

-- +goose Down
DROP TABLE IF EXISTS system_user_tool_auth;
