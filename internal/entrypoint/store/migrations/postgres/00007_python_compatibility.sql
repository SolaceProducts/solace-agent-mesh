-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'resource_access_shares'
      AND column_name = 'shared_at'
      AND data_type IN ('timestamp with time zone', 'timestamp without time zone')
  ) THEN
    ALTER TABLE resource_access_shares ALTER COLUMN shared_at DROP DEFAULT;
    ALTER TABLE resource_access_shares ALTER COLUMN updated_at DROP DEFAULT;
    ALTER TABLE resource_access_shares
      ALTER COLUMN shared_at TYPE BIGINT
      USING (EXTRACT(EPOCH FROM shared_at) * 1000)::BIGINT;
    ALTER TABLE resource_access_shares
      ALTER COLUMN updated_at TYPE BIGINT
      USING (EXTRACT(EPOCH FROM updated_at) * 1000)::BIGINT;
  END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
