-- +goose Up

ALTER TABLE gateways ADD COLUMN IF NOT EXISTS slug TEXT;
UPDATE gateways SET slug = id WHERE slug IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS ix_gateways_slug ON gateways(slug);

-- +goose Down

DROP INDEX IF EXISTS ix_gateways_slug;
ALTER TABLE gateways DROP COLUMN IF EXISTS slug;
