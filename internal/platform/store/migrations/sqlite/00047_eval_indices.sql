
-- +goose Up
CREATE INDEX IF NOT EXISTS ix_eval_experiments_target_agent
    ON eval_experiments(target_agent);
CREATE INDEX IF NOT EXISTS ix_eval_runs_run_group_started
    ON eval_runs(run_group_id, started_time);
DROP INDEX IF EXISTS ix_eval_runs_run_group_id;

-- +goose Down
CREATE INDEX IF NOT EXISTS ix_eval_runs_run_group_id
    ON eval_runs(run_group_id);
DROP INDEX IF EXISTS ix_eval_runs_run_group_started;
DROP INDEX IF EXISTS ix_eval_experiments_target_agent;
