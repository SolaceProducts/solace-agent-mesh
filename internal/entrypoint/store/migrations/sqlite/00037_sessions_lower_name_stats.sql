-- +goose Up

CREATE INDEX IF NOT EXISTS idx_sessions_lower_name ON sessions (LOWER(name));

-- +goose Down
DROP INDEX IF EXISTS idx_sessions_lower_name;
