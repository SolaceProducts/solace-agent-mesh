
-- +goose Up
CREATE TABLE IF NOT EXISTS user_identity_claims (
    user_id               TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    claim_key             TEXT NOT NULL,
    claim_value_canonical TEXT NOT NULL,
    verified              INTEGER NOT NULL DEFAULT 0,
    updated_time          INTEGER NOT NULL,
    PRIMARY KEY (user_id, claim_key, claim_value_canonical)
);

CREATE INDEX IF NOT EXISTS ix_user_identity_claims_lookup
    ON user_identity_claims(claim_key, claim_value_canonical);

-- +goose Down
DROP INDEX IF EXISTS ix_user_identity_claims_lookup;
DROP TABLE IF EXISTS user_identity_claims;
