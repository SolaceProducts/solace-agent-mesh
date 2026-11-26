
-- +goose Up
ALTER TABLE eval_experiments
    ADD COLUMN IF NOT EXISTS run_as TEXT;
ALTER TABLE eval_runs
    ADD COLUMN IF NOT EXISTS run_as TEXT;
ALTER TABLE eval_run_results
    ADD COLUMN IF NOT EXISTS failure_kind TEXT;

-- +goose Down
ALTER TABLE eval_run_results
    DROP COLUMN IF EXISTS failure_kind;
ALTER TABLE eval_runs
    DROP COLUMN IF EXISTS run_as;
ALTER TABLE eval_experiments
    DROP COLUMN IF EXISTS run_as;
