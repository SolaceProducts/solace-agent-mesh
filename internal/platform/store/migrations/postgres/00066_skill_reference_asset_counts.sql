
-- +goose Up
ALTER TABLE skills ADD COLUMN IF NOT EXISTS reference_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE skills ADD COLUMN IF NOT EXISTS asset_count INTEGER NOT NULL DEFAULT 0;
UPDATE skills SET reference_count = resource_count WHERE has_references = 1 AND has_assets = 0;
UPDATE skills SET asset_count = resource_count WHERE has_assets = 1 AND has_references = 0;

-- +goose Down
ALTER TABLE skills DROP COLUMN IF EXISTS asset_count;
ALTER TABLE skills DROP COLUMN IF EXISTS reference_count;
