-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='connectors' AND column_name='config_values') THEN
    ALTER TABLE connectors RENAME COLUMN config_values TO "values";
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='gateways' AND column_name='config_values') THEN
    ALTER TABLE gateways RENAME COLUMN config_values TO "values";
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='connectors' AND column_name='values') THEN
    ALTER TABLE connectors RENAME COLUMN "values" TO config_values;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='gateways' AND column_name='values') THEN
    ALTER TABLE gateways RENAME COLUMN "values" TO config_values;
  END IF;
END $$;
-- +goose StatementEnd
