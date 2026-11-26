-- +goose Up

ALTER TABLE eval_datasets ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;
ALTER TABLE eval_experiments ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE eval_experiments DROP COLUMN is_default;
ALTER TABLE eval_datasets DROP COLUMN is_default;
