
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
      WHERE table_name = 'entrypoint_identity_bindings' AND column_name = 'rotated_at'
  ) THEN
    ALTER TABLE entrypoint_identity_bindings ADD COLUMN rotated_at BIGINT;
  END IF;
END
$$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_lookup
    ON entrypoint_identity_bindings(lookup_key, lookup_value_canonical);

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_last_seen
    ON entrypoint_identity_bindings(last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_last_seen;
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_lookup;
ALTER TABLE entrypoint_identity_bindings DROP COLUMN IF EXISTS rotated_at;
