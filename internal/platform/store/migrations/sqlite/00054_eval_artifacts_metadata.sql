
-- +goose Up
ALTER TABLE eval_run_results ADD COLUMN artifacts_metadata TEXT;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN artifacts_metadata;
