-- +goose Up

CREATE TABLE IF NOT EXISTS product_analytics_inventory_claim (
    install_id   TEXT   PRIMARY KEY,
    claimed_day  TEXT   NOT NULL,
    updated_time BIGINT NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS product_analytics_inventory_claim;
