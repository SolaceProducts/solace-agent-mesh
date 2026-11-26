-- +goose Up

CREATE TABLE IF NOT EXISTS entrypoint_task_routing (
    task_id         TEXT PRIMARY KEY,
    gateway_id      TEXT NOT NULL,
    entrypoint_type TEXT NOT NULL,
    data            TEXT NOT NULL,
    expires_at      INTEGER NOT NULL,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_entrypoint_task_routing_expires_at ON entrypoint_task_routing(expires_at);

-- +goose Down

DROP TABLE IF EXISTS entrypoint_task_routing;
