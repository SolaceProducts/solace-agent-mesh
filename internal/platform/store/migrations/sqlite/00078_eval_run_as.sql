
-- +goose Up
ALTER TABLE eval_experiments ADD COLUMN run_as TEXT;
ALTER TABLE eval_runs ADD COLUMN run_as TEXT;
ALTER TABLE eval_run_results ADD COLUMN failure_kind TEXT;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN failure_kind;
ALTER TABLE eval_runs DROP COLUMN run_as;
ALTER TABLE eval_experiments DROP COLUMN run_as;
