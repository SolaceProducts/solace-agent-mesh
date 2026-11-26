-- +goose Up
ALTER TABLE eval_run_scores ADD COLUMN IF NOT EXISTS errored INTEGER;
ALTER TABLE eval_runs ADD COLUMN IF NOT EXISTS takeovers INTEGER NOT NULL DEFAULT 0;
ALTER TABLE eval_generation_operations ADD COLUMN IF NOT EXISTS request TEXT;
ALTER TABLE eval_generation_operations ADD COLUMN IF NOT EXISTS takeovers INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE eval_generation_operations DROP COLUMN IF EXISTS takeovers;
ALTER TABLE eval_generation_operations DROP COLUMN IF EXISTS request;
ALTER TABLE eval_runs DROP COLUMN IF EXISTS takeovers;
ALTER TABLE eval_run_scores DROP COLUMN IF EXISTS errored;
