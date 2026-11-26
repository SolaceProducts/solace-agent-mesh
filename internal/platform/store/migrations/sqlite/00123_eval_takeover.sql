-- +goose Up
ALTER TABLE eval_run_scores ADD COLUMN errored INTEGER;
ALTER TABLE eval_runs ADD COLUMN takeovers INTEGER NOT NULL DEFAULT 0;
ALTER TABLE eval_generation_operations ADD COLUMN request TEXT;
ALTER TABLE eval_generation_operations ADD COLUMN takeovers INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE eval_generation_operations DROP COLUMN takeovers;
ALTER TABLE eval_generation_operations DROP COLUMN request;
ALTER TABLE eval_runs DROP COLUMN takeovers;
ALTER TABLE eval_run_scores DROP COLUMN errored;
