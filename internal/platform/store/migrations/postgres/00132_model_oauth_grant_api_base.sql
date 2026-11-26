
-- +goose Up
ALTER TABLE model_oauth_grants ADD COLUMN IF NOT EXISTS api_base TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_oauth_grants DROP COLUMN IF EXISTS api_base;
