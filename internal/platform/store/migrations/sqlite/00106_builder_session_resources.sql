
-- +goose Up
CREATE TABLE IF NOT EXISTS builder_session_resources (
    actor              TEXT    NOT NULL,
    session_id         TEXT    NOT NULL,
    kind               TEXT    NOT NULL,
    resource_id        TEXT    NOT NULL,
    created_by_session INTEGER NOT NULL DEFAULT 0,
    created_time       BIGINT  NOT NULL,
    updated_time       BIGINT  NOT NULL,

    PRIMARY KEY (actor, session_id, kind, resource_id)
);

-- +goose Down
DROP TABLE IF EXISTS builder_session_resources;
