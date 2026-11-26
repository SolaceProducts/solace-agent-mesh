-- +goose Up

CREATE TABLE tool_auth_state (
    state        TEXT PRIMARY KEY,
    data         TEXT NOT NULL,
    expires_at   BIGINT NOT NULL,
    created_time BIGINT NOT NULL
);
CREATE INDEX idx_tool_auth_state_expires_at ON tool_auth_state(expires_at);

-- +goose Down

DROP TABLE tool_auth_state;
