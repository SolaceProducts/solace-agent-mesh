
-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_intents (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    object_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_time BIGINT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS deployment_intents;
