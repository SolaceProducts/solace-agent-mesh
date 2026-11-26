
-- +goose Up
ALTER TABLE eval_run_results
    ADD COLUMN input_tokens BIGINT,
    ADD COLUMN output_tokens BIGINT,
    ADD COLUMN cached_input_tokens BIGINT,
    ADD COLUMN reasoning_tokens BIGINT,
    ADD COLUMN total_tokens BIGINT;

-- +goose Down
ALTER TABLE eval_run_results
    DROP COLUMN total_tokens,
    DROP COLUMN reasoning_tokens,
    DROP COLUMN cached_input_tokens,
    DROP COLUMN output_tokens,
    DROP COLUMN input_tokens;
