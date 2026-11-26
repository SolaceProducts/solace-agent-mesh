-- +goose Up

ALTER TABLE sessions ADD COLUMN last_viewed_at INTEGER;
CREATE INDEX idx_sessions_last_viewed ON sessions(last_viewed_at);

UPDATE sessions SET last_viewed_at = updated_time WHERE last_viewed_at IS NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_sessions_last_viewed;
