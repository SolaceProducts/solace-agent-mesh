
-- +goose Up
ALTER TABLE eval_run_results ADD COLUMN input_tokens INTEGER;
ALTER TABLE eval_run_results ADD COLUMN output_tokens INTEGER;
ALTER TABLE eval_run_results ADD COLUMN cached_input_tokens INTEGER;
ALTER TABLE eval_run_results ADD COLUMN reasoning_tokens INTEGER;
ALTER TABLE eval_run_results ADD COLUMN total_tokens INTEGER;

-- +goose Down
ALTER TABLE eval_run_results DROP COLUMN total_tokens;
ALTER TABLE eval_run_results DROP COLUMN reasoning_tokens;
ALTER TABLE eval_run_results DROP COLUMN cached_input_tokens;
ALTER TABLE eval_run_results DROP COLUMN output_tokens;
ALTER TABLE eval_run_results DROP COLUMN input_tokens;
