-- +goose Up
ALTER TABLE gateways ADD COLUMN public_url TEXT;

-- +goose Down
