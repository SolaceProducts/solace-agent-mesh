-- +goose Up

ALTER TABLE finding_explanations ADD COLUMN IF NOT EXISTS finding_owner TEXT;

-- +goose Down

ALTER TABLE finding_explanations DROP COLUMN IF EXISTS finding_owner;
