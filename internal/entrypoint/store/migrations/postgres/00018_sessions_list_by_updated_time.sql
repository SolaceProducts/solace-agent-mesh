-- +goose Up

CREATE INDEX idx_sessions_list_updated ON sessions(user_id, deleted_at, updated_time DESC);
DROP INDEX IF EXISTS idx_sessions_list;

-- +goose Down

CREATE INDEX idx_sessions_list ON sessions(user_id, deleted_at, created_time DESC);
DROP INDEX IF EXISTS idx_sessions_list_updated;
