
-- +goose Up
ALTER TABLE model_configurations
  ADD COLUMN IF NOT EXISTS price_input BIGINT,
  ADD COLUMN IF NOT EXISTS price_output BIGINT
    CONSTRAINT model_configurations_price_pair
    CHECK ((price_input IS NULL) = (price_output IS NULL)),
  ADD COLUMN IF NOT EXISTS price_cache_read BIGINT,
  ADD COLUMN IF NOT EXISTS price_cache_write BIGINT
    CONSTRAINT model_configurations_price_cache_pair
    CHECK ((price_cache_read IS NULL) = (price_cache_write IS NULL)
      AND (price_cache_read IS NULL OR price_input IS NOT NULL));

-- +goose Down
ALTER TABLE model_configurations DROP COLUMN IF EXISTS price_cache_write;
ALTER TABLE model_configurations DROP COLUMN IF EXISTS price_cache_read;
ALTER TABLE model_configurations DROP COLUMN IF EXISTS price_output;
ALTER TABLE model_configurations DROP COLUMN IF EXISTS price_input;
