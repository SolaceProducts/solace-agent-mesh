-- +goose Up

ALTER TABLE finding_explanations ADD COLUMN finding_owner TEXT;

-- +goose Down

ALTER TABLE finding_explanations DROP COLUMN finding_owner;
