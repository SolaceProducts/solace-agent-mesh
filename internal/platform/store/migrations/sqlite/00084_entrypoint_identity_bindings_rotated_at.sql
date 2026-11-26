
-- +goose Up
ALTER TABLE entrypoint_identity_bindings ADD COLUMN rotated_at INTEGER;

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_lookup
    ON entrypoint_identity_bindings(lookup_key, lookup_value_canonical);

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_last_seen
    ON entrypoint_identity_bindings(last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_last_seen;
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_lookup;
ALTER TABLE entrypoint_identity_bindings DROP COLUMN rotated_at;
