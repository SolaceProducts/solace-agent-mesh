-- +goose Up

ALTER TABLE connectors DROP CONSTRAINT IF EXISTS check_connector_description_length;
ALTER TABLE connectors ADD CONSTRAINT check_connector_description_length
  CHECK (LENGTH(description) = 0 OR (LENGTH(description) >= 10 AND LENGTH(description) <= 1000));

-- +goose Down

ALTER TABLE connectors DROP CONSTRAINT IF EXISTS check_connector_description_length;
ALTER TABLE connectors ADD CONSTRAINT check_connector_description_length
  CHECK (description IS NULL OR (LENGTH(description) >= 10 AND LENGTH(description) <= 1000));
