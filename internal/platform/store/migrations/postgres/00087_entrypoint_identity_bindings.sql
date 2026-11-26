
-- +goose Up
CREATE TABLE IF NOT EXISTS entrypoint_identity_bindings (
    gateway_id             TEXT NOT NULL,
    lookup_key             TEXT NOT NULL,
    lookup_value_canonical TEXT NOT NULL,
    user_id                UUID REFERENCES users(id) ON DELETE CASCADE,
    continuity_token       TEXT NOT NULL DEFAULT '',
    corroborated_at        BIGINT,
    last_seen_at           BIGINT NOT NULL,
    PRIMARY KEY (gateway_id, lookup_key, lookup_value_canonical)
);

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_user
    ON entrypoint_identity_bindings(user_id);

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_lookup
    ON entrypoint_identity_bindings(lookup_key, lookup_value_canonical);

CREATE INDEX IF NOT EXISTS ix_entrypoint_identity_bindings_last_seen
    ON entrypoint_identity_bindings(last_seen_at);

-- +goose Down
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_last_seen;
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_lookup;
DROP INDEX IF EXISTS ix_entrypoint_identity_bindings_user;
DROP TABLE IF EXISTS entrypoint_identity_bindings;
