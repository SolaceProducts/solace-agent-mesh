-- +goose Up

ALTER TABLE product_analytics_state ADD COLUMN binding_secret TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE product_analytics_state DROP COLUMN binding_secret;
