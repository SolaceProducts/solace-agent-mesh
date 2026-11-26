
-- +goose Up
UPDATE model_configurations
SET alias = LOWER(alias)
WHERE LOWER(alias) IN ('general', 'planning', 'image_gen', 'report_gen')
  AND alias != LOWER(alias)
  AND NOT EXISTS (
      SELECT 1 FROM model_configurations existing
      WHERE existing.alias = LOWER(model_configurations.alias)
  );

-- +goose Down
