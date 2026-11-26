
-- +goose Up
CREATE TABLE platform_identity_config (
    id            TEXT    PRIMARY KEY CHECK (id = 'singleton'),
    toolset_id    TEXT,
    lookup_key    TEXT    NOT NULL DEFAULT 'email',
    created_by    TEXT    NOT NULL DEFAULT '',
    updated_by    TEXT    NOT NULL DEFAULT '',
    created_time  BIGINT  NOT NULL DEFAULT 0,
    updated_time  BIGINT  NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE platform_identity_config;
