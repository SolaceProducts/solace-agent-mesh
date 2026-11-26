-- +goose Up

CREATE TABLE IF NOT EXISTS product_analytics_state (
    id            TEXT    PRIMARY KEY CHECK (id = 'singleton'),
    install_id    TEXT    NOT NULL,
    consent_state TEXT    NOT NULL,
    created_time  INTEGER NOT NULL,
    updated_time  INTEGER NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS product_analytics_state;
