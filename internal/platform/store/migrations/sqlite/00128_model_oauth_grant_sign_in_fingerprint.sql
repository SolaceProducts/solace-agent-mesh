
-- +goose Up
ALTER TABLE model_oauth_grants ADD COLUMN sign_in_fingerprint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_oauth_grants DROP COLUMN sign_in_fingerprint;
