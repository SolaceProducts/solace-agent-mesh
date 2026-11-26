
-- +goose Up
ALTER TABLE model_configurations ADD COLUMN price_input BIGINT;
ALTER TABLE model_configurations ADD COLUMN price_output BIGINT;
ALTER TABLE model_configurations ADD COLUMN price_cache_read BIGINT;
ALTER TABLE model_configurations ADD COLUMN price_cache_write BIGINT;

-- +goose Down
ALTER TABLE model_configurations DROP COLUMN price_cache_write;
ALTER TABLE model_configurations DROP COLUMN price_cache_read;
ALTER TABLE model_configurations DROP COLUMN price_output;
ALTER TABLE model_configurations DROP COLUMN price_input;
