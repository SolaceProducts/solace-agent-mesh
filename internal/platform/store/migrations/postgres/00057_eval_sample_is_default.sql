-- +goose Up

ALTER TABLE eval_datasets ADD COLUMN IF NOT EXISTS is_default INTEGER NOT NULL DEFAULT 0;
ALTER TABLE eval_experiments ADD COLUMN IF NOT EXISTS is_default INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE eval_experiments DROP COLUMN IF EXISTS is_default;
ALTER TABLE eval_datasets DROP COLUMN IF EXISTS is_default;
