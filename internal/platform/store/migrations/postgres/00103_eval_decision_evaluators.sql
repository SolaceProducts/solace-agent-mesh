-- +goose Up
ALTER TABLE eval_evaluators ADD COLUMN IF NOT EXISTS decision_config TEXT;
ALTER TABLE eval_run_scores ADD COLUMN IF NOT EXISTS details TEXT;
ALTER TABLE eval_runs ADD COLUMN IF NOT EXISTS evaluator_stats TEXT;

-- +goose Down
ALTER TABLE eval_runs DROP COLUMN IF EXISTS evaluator_stats;
ALTER TABLE eval_run_scores DROP COLUMN IF EXISTS details;
ALTER TABLE eval_evaluators DROP COLUMN IF EXISTS decision_config;
