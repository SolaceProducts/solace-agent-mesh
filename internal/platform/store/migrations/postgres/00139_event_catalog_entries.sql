
-- +goose Up
CREATE TABLE IF NOT EXISTS event_catalog_entries (
    id              UUID PRIMARY KEY,
    name            TEXT NOT NULL,
    event_key       TEXT NOT NULL UNIQUE,
    description     TEXT NOT NULL,
    entrypoint_id   UUID NOT NULL,
    topic_pattern   TEXT NOT NULL,
    payload_schema  TEXT,
    example_payload TEXT,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_by      TEXT NOT NULL,
    updated_by      TEXT NOT NULL,
    created_time    BIGINT NOT NULL,
    updated_time    BIGINT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_event_catalog_entries_name_lower ON event_catalog_entries (LOWER(name));
CREATE INDEX IF NOT EXISTS ix_event_catalog_entries_entrypoint_id ON event_catalog_entries (entrypoint_id);

-- +goose Down
DROP INDEX IF EXISTS ix_event_catalog_entries_entrypoint_id;
DROP INDEX IF EXISTS ux_event_catalog_entries_name_lower;
DROP TABLE IF EXISTS event_catalog_entries;
