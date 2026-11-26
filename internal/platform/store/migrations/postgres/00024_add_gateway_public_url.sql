-- +goose Up
ALTER TABLE gateways ADD COLUMN IF NOT EXISTS public_url TEXT;

-- +goose Down
ALTER TABLE gateways DROP COLUMN IF EXISTS public_url;
