-- +goose Up
-- +goose NO TRANSACTION
ALTER TABLE connectors RENAME COLUMN config_values TO "values";
ALTER TABLE gateways RENAME COLUMN config_values TO "values";

-- +goose Down
-- +goose NO TRANSACTION
ALTER TABLE connectors RENAME COLUMN "values" TO config_values;
ALTER TABLE gateways RENAME COLUMN "values" TO config_values;
