-- +goose Up
ALTER TABLE eval_experiments ADD COLUMN IF NOT EXISTS target_type TEXT NOT NULL DEFAULT 'agent';
ALTER TABLE eval_examples ADD COLUMN IF NOT EXISTS input_data TEXT;
ALTER TABLE eval_run_results ADD COLUMN IF NOT EXISTS target_type TEXT;
ALTER TABLE eval_run_results ADD COLUMN IF NOT EXISTS token_breakdown TEXT;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN IF EXISTS token_breakdown;
ALTER TABLE eval_run_results DROP COLUMN IF EXISTS target_type;
ALTER TABLE eval_examples DROP COLUMN IF EXISTS input_data;
ALTER TABLE eval_experiments DROP COLUMN IF EXISTS target_type;
