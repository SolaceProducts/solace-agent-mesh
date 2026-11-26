-- +goose Up
ALTER TABLE eval_evaluators ADD COLUMN decision_config TEXT;
ALTER TABLE eval_run_scores ADD COLUMN details TEXT;
ALTER TABLE eval_runs ADD COLUMN evaluator_stats TEXT;

-- +goose Down
ALTER TABLE eval_runs DROP COLUMN evaluator_stats;
ALTER TABLE eval_run_scores DROP COLUMN details;
ALTER TABLE eval_evaluators DROP COLUMN decision_config;
