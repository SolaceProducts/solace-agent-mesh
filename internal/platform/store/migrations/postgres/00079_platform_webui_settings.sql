
-- +goose Up
CREATE TABLE platform_webui_settings (
    id               TEXT    PRIMARY KEY CHECK (id = 'singleton'),
    app_name         TEXT,
    welcome_message  TEXT,
    collect_feedback INTEGER,
    disclaimer_text  TEXT,
    logo_url         TEXT,
    small_logo_url   TEXT,
    system_purpose   TEXT,
    response_format  TEXT,
    created_by       TEXT   NOT NULL DEFAULT '',
    updated_by       TEXT   NOT NULL DEFAULT '',
    created_time     BIGINT NOT NULL DEFAULT 0,
    updated_time     BIGINT NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE platform_webui_settings;
