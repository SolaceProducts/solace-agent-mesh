
-- +goose Up
ALTER TABLE model_oauth_grants ADD COLUMN IF NOT EXISTS sign_in_fingerprint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_oauth_grants DROP COLUMN IF EXISTS sign_in_fingerprint;
