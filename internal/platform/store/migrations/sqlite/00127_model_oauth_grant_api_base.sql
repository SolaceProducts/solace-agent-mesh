
-- +goose Up
ALTER TABLE model_oauth_grants ADD COLUMN api_base TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE model_oauth_grants DROP COLUMN api_base;
