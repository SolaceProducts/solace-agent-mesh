-- +goose Up
ALTER TABLE eval_experiments ADD COLUMN target_type TEXT NOT NULL DEFAULT 'agent';
ALTER TABLE eval_examples ADD COLUMN input_data TEXT;
ALTER TABLE eval_run_results ADD COLUMN target_type TEXT;
ALTER TABLE eval_run_results ADD COLUMN token_breakdown TEXT;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN token_breakdown;
ALTER TABLE eval_run_results DROP COLUMN target_type;
ALTER TABLE eval_examples DROP COLUMN input_data;
ALTER TABLE eval_experiments DROP COLUMN target_type;
