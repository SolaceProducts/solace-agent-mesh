
-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_requests (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    object_id TEXT NOT NULL,
    op TEXT NOT NULL,
    args TEXT NOT NULL,
    created_time BIGINT NOT NULL,
    taken INTEGER NOT NULL DEFAULT 0,
    done_time BIGINT,
    answer TEXT
);

CREATE INDEX IF NOT EXISTS idx_deployment_requests_waiting ON deployment_requests (kind, created_time) WHERE done_time IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_deployment_requests_waiting;
DROP TABLE IF EXISTS deployment_requests;
