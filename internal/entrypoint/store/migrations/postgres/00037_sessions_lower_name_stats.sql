-- +goose NO TRANSACTION
-- +goose Up

DROP INDEX CONCURRENTLY IF EXISTS idx_sessions_lower_name;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sessions_lower_name ON sessions (LOWER(name));

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_sessions_lower_name;
